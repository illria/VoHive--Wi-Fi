package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iniwex5/vohive/internal/global"
	"github.com/iniwex5/vohive/pkg/logger"
	"golang.org/x/mod/semver"
)

const (
	DefaultRepoOwner = "illria"
	DefaultRepoName  = "VoHive--Wi-Fi"
	DefaultChannel   = "stable"

	defaultAPIBaseURL      = "https://api.github.com"
	defaultRequestTimeout  = 20 * time.Second
	defaultSignalDelay     = 2 * time.Second
	defaultMinimumFileSize = 64 * 1024
	defaultMaximumFileSize = 128 * 1024 * 1024
)

type Channel string

const (
	ChannelStable      Channel = "stable"
	ChannelPrerelease  Channel = "prerelease"
)

type UpdateState string

const (
	StateIdle        UpdateState = "idle"
	StateChecking    UpdateState = "checking"
	StateAvailable   UpdateState = "available"
	StateDownloading UpdateState = "downloading"
	StateVerifying   UpdateState = "verifying"
	StateBackingUp   UpdateState = "backing_up"
	StateApplying    UpdateState = "applying"
	StateRestarting  UpdateState = "restarting"
	StateSuccess     UpdateState = "success"
	StateFailed      UpdateState = "failed"
	StateRolledBack  UpdateState = "rolled_back"
)

type ErrorCode string

const (
	ErrUpdateDisabled          ErrorCode = "update_disabled"
	ErrUpdateInProgress        ErrorCode = "update_in_progress"
	ErrGitHubUnreachable       ErrorCode = "github_unreachable"
	ErrReleaseNotFound         ErrorCode = "release_not_found"
	ErrNoUpdate                ErrorCode = "no_update_available"
	ErrInvalidCurrentVersion   ErrorCode = "invalid_current_version"
	ErrUnsupportedArchitecture ErrorCode = "unsupported_architecture"
	ErrAssetNotFound           ErrorCode = "asset_not_found"
	ErrChecksumAssetNotFound   ErrorCode = "checksum_asset_not_found"
	ErrDownloadFailed          ErrorCode = "download_failed"
	ErrChecksumMismatch        ErrorCode = "checksum_mismatch"
	ErrBackupFailed            ErrorCode = "backup_failed"
	ErrReplaceFailed           ErrorCode = "replace_failed"
	ErrRollbackFailed          ErrorCode = "rollback_failed"
	ErrDockerUnsupported       ErrorCode = "docker_update_unsupported"
	ErrRestartFailed           ErrorCode = "restart_failed"
	ErrInvalidGitHubProxy      ErrorCode = "invalid_github_proxy"
)

type UpdateError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *UpdateError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return e.Message
	}
	if e.Message == "" {
		return e.Cause.Error()
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Cause)
}

func (e *UpdateError) Unwrap() error { return e.Cause }

func newUpdateError(code ErrorCode, message string, cause error) error {
	return &UpdateError{Code: code, Message: message, Cause: cause}
}

func ErrorCodeOf(err error) string {
	if err == nil {
		return ""
	}
	var updateErr *UpdateError
	if errors.As(err, &updateErr) {
		return string(updateErr.Code)
	}
	return string(ErrDownloadFailed)
}

type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Published  string  `json:"published_at"`
	Assets     []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type UpdateInfo struct {
	HasUpdate         bool          `json:"has_update"`
	CurrentVer        string        `json:"current_version"`
	LatestVer         string        `json:"latest_version"`
	ReleaseNote       string        `json:"release_note"`
	IsDocker          bool          `json:"is_docker"`
	MigrationRequired bool          `json:"migration_required"`
	Supported         bool          `json:"supported"`
	Channel           string        `json:"channel"`
	ProxyID           string        `json:"proxy_id"`
	ProxyOptions      []ProxyOption `json:"proxy_options"`
	ErrorCode         string        `json:"error_code,omitempty"`
}

type UpdateStatus struct {
	State          UpdateState `json:"state"`
	CurrentVersion string      `json:"current_version"`
	TargetVersion  string      `json:"target_version"`
	ProxyID        string      `json:"proxy_id,omitempty"`
	Progress       int         `json:"progress"`
	Message        string      `json:"message"`
	Error          string      `json:"error"`
	ErrorCode      string      `json:"error_code"`
	BackupPath     string      `json:"backup_path,omitempty"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type Options struct {
	RepoOwner       string
	RepoName        string
	Channel         Channel
	AllowPrerelease bool
	APIBaseURL      string
	HTTPClient      *http.Client
	Executable      func() (string, error)
	Signal          func(os.Signal) error
	IsDocker        func() bool
	Now             func() time.Time
	MinBinarySize   int64
	MaxBinarySize   int64
	SignalDelay     time.Duration
}

type Manager struct {
	mu sync.Mutex

	state   UpdateStatus
	running bool

	repoOwner       string
	repoName        string
	channel         Channel
	allowPrerelease bool
	apiBaseURL      string
	httpClient      *http.Client
	executable      func() (string, error)
	signal          func(os.Signal) error
	isDocker        func() bool
	now             func() time.Time
	minBinarySize   int64
	maxBinarySize   int64
	signalDelay     time.Duration
}

func NewManager(options Options) *Manager {
	if options.RepoOwner == "" {
		options.RepoOwner = DefaultRepoOwner
	}
	if options.RepoName == "" {
		options.RepoName = DefaultRepoName
	}
	if options.Channel == "" {
		options.Channel = ChannelStable
	}
	if options.APIBaseURL == "" {
		options.APIBaseURL = defaultAPIBaseURL
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	if options.Executable == nil {
		options.Executable = os.Executable
	}
	if options.Signal == nil {
		options.Signal = func(signal os.Signal) error {
			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				return err
			}
			return process.Signal(signal)
		}
	}
	if options.IsDocker == nil {
		options.IsDocker = func() bool {
			_, err := os.Stat("/.dockerenv")
			return err == nil
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MinBinarySize <= 0 {
		options.MinBinarySize = defaultMinimumFileSize
	}
	if options.MaxBinarySize <= 0 {
		options.MaxBinarySize = defaultMaximumFileSize
	}
	if options.SignalDelay <= 0 {
		options.SignalDelay = defaultSignalDelay
	}

	m := &Manager{
		repoOwner:       options.RepoOwner,
		repoName:        options.RepoName,
		channel:         options.Channel,
		allowPrerelease: options.AllowPrerelease,
		apiBaseURL:      strings.TrimRight(options.APIBaseURL, "/"),
		httpClient:      options.HTTPClient,
		executable:      options.Executable,
		signal:          options.Signal,
		isDocker:        options.IsDocker,
		now:             options.Now,
		minBinarySize:   options.MinBinarySize,
		maxBinarySize:   options.MaxBinarySize,
		signalDelay:     options.SignalDelay,
		state: UpdateStatus{
			State:          StateIdle,
			CurrentVersion: strings.TrimSpace(global.Version),
			UpdatedAt:      options.Now(),
		},
	}
	m.loadPersistedStatus()
	if m.state.State == StateRestarting {
		m.armStartupHealthWindow()
	}
	return m
}

var defaultManager = NewManager(Options{})

func CheckUpdate() (*UpdateInfo, error) { return defaultManager.CheckUpdate() }

func CheckUpdateWithProxy(proxyID string) (*UpdateInfo, error) {
	return defaultManager.CheckUpdateWithProxy(proxyID)
}

func ApplyUpdate() error {
	_, err := defaultManager.StartUpdate()
	return err
}

func StartUpdate() (UpdateStatus, error) { return defaultManager.StartUpdate() }

func StartUpdateWithProxy(proxyID string) (UpdateStatus, error) {
	return defaultManager.StartUpdateWithProxy(proxyID)
}

func CurrentStatus() UpdateStatus { return defaultManager.Status() }

func MarkStartupHealthy() { defaultManager.MarkStartupHealthy() }

func (m *Manager) Status() UpdateStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) CheckUpdate() (*UpdateInfo, error) {
	return m.CheckUpdateWithProxy(ProxyAuto)
}

func (m *Manager) CheckUpdateWithProxy(proxyID string) (*UpdateInfo, error) {
	release, proxy, err := m.fetchReleaseWithProxy(proxyID)
	if err != nil {
		return nil, err
	}

	latestVersion, ok := normalizeVersion(release.TagName)
	if !ok {
		return nil, newUpdateError(ErrReleaseNotFound, "远端 Release 不是合法 SemVer", nil)
	}
	currentRaw := strings.TrimSpace(global.Version)
	currentVersion, currentValid := normalizeVersion(currentRaw)
	legacy := isLegacyVersion(currentRaw)
	if !currentValid && !legacy {
		return nil, newUpdateError(ErrInvalidCurrentVersion, "当前版本不是合法 SemVer", nil)
	}

	_, supported := runtimeAssetKey()
	hasUpdate := false
	if legacy {
		hasUpdate = true
	} else if semver.Compare(currentVersion, latestVersion) < 0 {
		hasUpdate = true
	}

	return &UpdateInfo{
		HasUpdate:         hasUpdate,
		CurrentVer:        displayVersion(currentRaw, currentVersion),
		LatestVer:         latestVersion,
		ReleaseNote:       release.Body,
		IsDocker:          m.isDocker(),
		MigrationRequired: legacy,
		Supported:         supported,
		Channel:           string(m.channel),
		ProxyID:           proxy.ID,
		ProxyOptions:      GitHubProxyOptions(),
	}, nil
}

func (m *Manager) StartUpdate() (UpdateStatus, error) {
	return m.StartUpdateWithProxy(ProxyAuto)
}

func (m *Manager) StartUpdateWithProxy(proxyID string) (UpdateStatus, error) {
	m.mu.Lock()
	if m.running || updateInProgress(m.state.State) {
		status := m.state
		m.mu.Unlock()
		return status, newUpdateError(ErrUpdateInProgress, "已有更新任务正在运行", nil)
	}
	if m.isDocker() {
		status := m.state
		m.mu.Unlock()
		return status, newUpdateError(ErrDockerUnsupported, "容器环境不支持直接替换运行中的二进制", nil)
	}
	m.running = true
	m.state = UpdateStatus{
		State:          StateChecking,
		CurrentVersion: strings.TrimSpace(global.Version),
		ProxyID:        normalizeProxyID(proxyID),
		Progress:       0,
		Message:        "正在检查更新",
		UpdatedAt:      m.now(),
	}
	status := m.state
	m.mu.Unlock()
	m.persistStatus(status)

	go m.runUpdate(proxyID)
	return status, nil
}

func (m *Manager) runUpdate(proxyID string) {
	release, proxy, err := m.fetchReleaseWithProxy(proxyID)
	if err != nil {
		m.fail(err)
		return
	}
	latestVersion, ok := normalizeVersion(release.TagName)
	if !ok {
		m.fail(newUpdateError(ErrReleaseNotFound, "远端 Release 不是合法 SemVer", nil))
		return
	}
	currentRaw := strings.TrimSpace(global.Version)
	currentVersion, currentValid := normalizeVersion(currentRaw)
	if !currentValid && !isLegacyVersion(currentRaw) {
		m.fail(newUpdateError(ErrInvalidCurrentVersion, "当前版本不是合法 SemVer", nil))
		return
	}
	if currentValid && semver.Compare(currentVersion, latestVersion) >= 0 {
		m.fail(newUpdateError(ErrNoUpdate, "当前版本不低于远端 Release", nil))
		return
	}
	m.mu.Lock()
	m.state.ProxyID = proxy.ID
	m.mu.Unlock()
	m.setState(StateAvailable, 0, "发现可用更新", latestVersion)

	arch, supported := runtimeAssetKey()
	if !supported {
		m.fail(newUpdateError(ErrUnsupportedArchitecture, "当前系统架构没有对应 Release 资产", nil))
		return
	}
	binaryName := fmt.Sprintf("vohive_%s_%s_%s", release.TagName, runtime.GOOS, arch)
	binaryAsset, checksumAsset, err := findAssets(release.Assets, binaryName)
	if err != nil {
		m.fail(err)
		return
	}

	executable, err := m.executable()
	if err != nil {
		m.fail(newUpdateError(ErrBackupFailed, "无法定位当前可执行文件", err))
		return
	}
	updateRoot := filepath.Join(filepath.Dir(executable), "update")
	downloadDir := filepath.Join(updateRoot, "downloads")
	backupDir := filepath.Join(updateRoot, "backup")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		m.fail(newUpdateError(ErrDownloadFailed, "创建更新下载目录失败", err))
		return
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		m.fail(newUpdateError(ErrBackupFailed, "创建更新备份目录失败", err))
		return
	}

	downloadPath, proxy, err := m.downloadAndVerifyAssets(binaryAsset, checksumAsset, downloadDir, latestVersion, proxyID)
	if err != nil {
		m.fail(err)
		return
	}
	defer os.Remove(downloadPath)
	m.mu.Lock()
	m.state.ProxyID = proxy.ID
	m.mu.Unlock()

	m.setState(StateBackingUp, 0, "正在备份当前版本", latestVersion)
	m.setState(StateApplying, 0, "正在替换当前版本", latestVersion)
	backupPath, err := m.replaceBinary(downloadPath, executable, backupDir)
	if err != nil {
		if ErrorCodeOf(err) == string(ErrRollbackFailed) {
			m.fail(err)
		} else {
			m.rollbackState(err)
		}
		return
	}

	m.mu.Lock()
	m.state.BackupPath = backupPath
	m.mu.Unlock()
	m.setState(StateRestarting, 100, "更新已应用，等待服务重启", latestVersion)

	go func() {
		if m.signalDelay > 0 {
			time.Sleep(m.signalDelay)
		}
		if err := m.signal(syscall.SIGTERM); err != nil {
			m.fail(newUpdateError(ErrRestartFailed, "发送重启信号失败", err))
		}
	}()
}

func (m *Manager) fetchRelease() (Release, error) {
	release, _, err := m.fetchReleaseWithProxy(ProxyAuto)
	return release, err
}

func (m *Manager) fetchReleaseWithProxy(proxyID string) (Release, githubProxy, error) {
	candidates, ok := proxyCandidates(proxyID)
	if !ok {
		return Release{}, githubProxy{}, newUpdateError(ErrInvalidGitHubProxy, "未知的 GitHub 加速入口", nil)
	}
	var lastErr error
	for _, proxy := range candidates {
		release, err := m.fetchReleaseViaProxy(proxy)
		if err == nil {
			return release, proxy, nil
		}
		lastErr = err
		logger.Warn("GitHub 更新入口不可用，尝试下一个入口", "proxy", proxy.ID, "err", err)
	}
	if lastErr == nil {
		lastErr = newUpdateError(ErrGitHubUnreachable, "没有可用的 GitHub 更新入口", nil)
	}
	return Release{}, githubProxy{}, lastErr
}

func (m *Manager) fetchReleaseViaProxy(proxy githubProxy) (Release, error) {
	if m.channel == ChannelPrerelease {
		var releases []Release
		if err := m.getJSONWithProxy(proxy, "/repos/"+m.repoOwner+"/"+m.repoName+"/releases?per_page=100", &releases, ErrGitHubUnreachable); err != nil {
			return Release{}, err
		}
		return selectPrereleaseRelease(releases)
	}

	var release Release
	if err := m.getJSONWithProxy(proxy, "/repos/"+m.repoOwner+"/"+m.repoName+"/releases/latest", &release, ErrGitHubUnreachable); err != nil {
		return Release{}, err
	}
	if release.Draft || release.Prerelease && !m.allowPrerelease {
		return Release{}, newUpdateError(ErrReleaseNotFound, "没有可用的稳定 Release", nil)
	}
	normalizedTag, ok := normalizeVersion(release.TagName)
	if !ok {
		return Release{}, newUpdateError(ErrReleaseNotFound, "稳定 Release 标签不是合法 SemVer", nil)
	}
	release.TagName = normalizedTag
	return release, nil
}

func (m *Manager) getJSON(path string, target any, networkCode ErrorCode) error {
	return m.getJSONWithProxy(githubProxy{}, path, target, networkCode)
}

func (m *Manager) getJSONWithProxy(proxy githubProxy, path string, target any, networkCode ErrorCode) error {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rewriteGitHubURL(proxy, m.apiBaseURL+path), nil)
	if err != nil {
		return newUpdateError(networkCode, "创建 GitHub 请求失败", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "VoHive-Updater")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return newUpdateError(networkCode, "访问 GitHub 失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return newUpdateError(ErrReleaseNotFound, "GitHub Release 不存在", nil)
	}
	if response.StatusCode != http.StatusOK {
		return newUpdateError(networkCode, fmt.Sprintf("GitHub 返回 HTTP %d", response.StatusCode), nil)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(target); err != nil {
		return newUpdateError(networkCode, "解析 GitHub Release 响应失败", err)
	}
	return nil
}

func selectPrereleaseRelease(releases []Release) (Release, error) {
	var selected *Release
	for i := range releases {
		release := releases[i]
		if release.Draft || !release.Prerelease {
			continue
		}
		version, ok := normalizeVersion(release.TagName)
		if !ok {
			continue
		}
		release.TagName = version
		if selected == nil || semver.Compare(version, selected.TagName) > 0 {
			candidate := release
			selected = &candidate
		}
	}
	if selected == nil {
		return Release{}, newUpdateError(ErrReleaseNotFound, "没有可用的预发布 Release", nil)
	}
	return *selected, nil
}

func normalizeVersion(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.HasPrefix(raw, "v") {
		raw = "v" + raw
	}
	if !semver.IsValid(raw) {
		return "", false
	}
	return raw, true
}

func displayVersion(raw, normalized string) string {
	if normalized != "" {
		return normalized
	}
	return strings.TrimSpace(raw)
}

func isLegacyVersion(version string) bool {
	switch strings.ToLower(strings.TrimSpace(version)) {
	case "portable", "unknown":
		return true
	default:
		return false
	}
}

func assetKey(goos, goarch, goarm string) (string, bool) {
	if goos != "linux" {
		return "", false
	}
	switch goarch {
	case "amd64":
		return "amd64", true
	case "arm64":
		return "arm64", true
	case "arm":
		if goarm == "7" || goarm == "" {
			return "armv7", true
		}
	}
	return "", false
}

func runtimeAssetKey() (string, bool) {
	// GOARM selects the arm build at compile time but is not exposed by runtime.
	// The release matrix only publishes the supported armv7 variant.
	return assetKey(runtime.GOOS, runtime.GOARCH, "7")
}

func findAssets(assets []Asset, binaryName string) (Asset, Asset, error) {
	var binary Asset
	var checksum Asset
	for _, asset := range assets {
		switch asset.Name {
		case binaryName:
			binary = asset
		case binaryName + ".sha256":
			checksum = asset
		}
	}
	if binary.Name == "" || binary.BrowserDownloadURL == "" {
		return Asset{}, Asset{}, newUpdateError(ErrAssetNotFound, "找不到当前架构的裸二进制资产", nil)
	}
	if checksum.Name == "" || checksum.BrowserDownloadURL == "" {
		return Asset{}, Asset{}, newUpdateError(ErrChecksumAssetNotFound, "找不到裸二进制对应的 SHA-256 资产", nil)
	}
	return binary, checksum, nil
}

func (m *Manager) downloadAndVerifyAssets(binaryAsset, checksumAsset Asset, directory, targetVersion, proxyID string) (string, githubProxy, error) {
	candidates, ok := proxyCandidates(proxyID)
	if !ok {
		return "", githubProxy{}, newUpdateError(ErrInvalidGitHubProxy, "未知的 GitHub 加速入口", nil)
	}

	var lastErr error
	for _, proxy := range candidates {
		m.setState(StateDownloading, 0, fmt.Sprintf("正在通过 %s 下载更新", proxy.Name), targetVersion)
		downloadPath, err := m.downloadBinaryWithProxy(binaryAsset, directory, targetVersion, proxy)
		if err != nil {
			lastErr = err
			logger.Warn("GitHub 更新资产下载失败，尝试下一个入口", "proxy", proxy.ID, "err", err)
			continue
		}

		m.setState(StateVerifying, 0, fmt.Sprintf("正在通过 %s 验证 SHA-256", proxy.Name), targetVersion)
		checksum, err := m.downloadChecksumWithProxy(checksumAsset, proxy)
		if err == nil {
			err = verifyChecksum(downloadPath, checksum)
		}
		if err == nil {
			m.setState(StateVerifying, 100, "SHA-256 校验通过", targetVersion)
			return downloadPath, proxy, nil
		}

		_ = os.Remove(downloadPath)
		lastErr = err
		logger.Warn("GitHub 更新资产校验失败，尝试下一个入口", "proxy", proxy.ID, "err", err)
	}
	if lastErr == nil {
		lastErr = newUpdateError(ErrDownloadFailed, "没有可用的 GitHub 下载入口", nil)
	}
	return "", githubProxy{}, lastErr
}

func (m *Manager) downloadBinary(asset Asset, directory, targetVersion string) (string, error) {
	return m.downloadBinaryWithProxy(asset, directory, targetVersion, githubProxy{})
}

func (m *Manager) downloadBinaryWithProxy(asset Asset, directory, targetVersion string, proxy githubProxy) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rewriteGitHubURL(proxy, asset.BrowserDownloadURL), nil)
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "创建二进制下载请求失败", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "VoHive-Updater")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "下载二进制失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", newUpdateError(ErrDownloadFailed, fmt.Sprintf("二进制下载返回 HTTP %d", response.StatusCode), nil)
	}
	if response.ContentLength > m.maxBinarySize {
		return "", newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
	}

	temporary, err := os.CreateTemp(directory, ".vohive-update-*")
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "创建二进制临时文件失败", err)
	}
	path := temporary.Name()
	removeTemporary := true
	defer func() {
		temporary.Close()
		if removeTemporary {
			os.Remove(path)
		}
	}()

	writer := &progressWriter{manager: m, writer: temporary, total: response.ContentLength, target: targetVersion}
	bytesWritten, err := io.Copy(writer, io.LimitReader(response.Body, m.maxBinarySize+1))
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "写入二进制临时文件失败", err)
	}
	if bytesWritten > m.maxBinarySize {
		return "", newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
	}
	if bytesWritten < m.minBinarySize {
		return "", newUpdateError(ErrDownloadFailed, "下载文件过小，拒绝替换当前程序", nil)
	}
	if err := temporary.Chmod(0o755); err != nil {
		return "", newUpdateError(ErrDownloadFailed, "设置更新文件权限失败", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", newUpdateError(ErrDownloadFailed, "同步更新文件失败", err)
	}
	if err := temporary.Close(); err != nil {
		return "", newUpdateError(ErrDownloadFailed, "关闭更新文件失败", err)
	}
	removeTemporary = false
	return path, nil
}

type progressWriter struct {
	manager *Manager
	writer  io.Writer
	total   int64
	target  string
	written int64
}

func (w *progressWriter) Write(data []byte) (int, error) {
	count, err := w.writer.Write(data)
	if err != nil {
		return count, err
	}
	w.written += int64(count)
	progress := 0
	if w.total > 0 {
		progress = int((w.written * 100) / w.total)
		if progress > 100 {
			progress = 100
		}
	}
	w.manager.setState(StateDownloading, progress, "正在下载更新", w.target)
	return count, nil
}

func (m *Manager) downloadChecksum(asset Asset) (string, error) {
	return m.downloadChecksumWithProxy(asset, githubProxy{})
}

func (m *Manager) downloadChecksumWithProxy(asset Asset, proxy githubProxy) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rewriteGitHubURL(proxy, asset.BrowserDownloadURL), nil)
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "创建 SHA-256 下载请求失败", err)
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "VoHive-Updater")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "下载 SHA-256 文件失败", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", newUpdateError(ErrChecksumAssetNotFound, "SHA-256 文件不存在", nil)
	}
	if response.StatusCode != http.StatusOK {
		return "", newUpdateError(ErrDownloadFailed, fmt.Sprintf("SHA-256 下载返回 HTTP %d", response.StatusCode), nil)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "读取 SHA-256 文件失败", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return "", newUpdateError(ErrChecksumMismatch, "SHA-256 文件格式无效", nil)
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", newUpdateError(ErrChecksumMismatch, "SHA-256 文件格式无效", err)
	}
	return strings.ToLower(fields[0]), nil
}

func verifyChecksum(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return newUpdateError(ErrChecksumMismatch, "打开待验证文件失败", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return newUpdateError(ErrChecksumMismatch, "计算本地 SHA-256 失败", err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return newUpdateError(ErrChecksumMismatch, "SHA-256 校验不一致", nil)
	}
	return nil
}

func (m *Manager) replaceBinary(source, executable, backupDirectory string) (string, error) {
	if err := os.Chmod(source, 0o755); err != nil {
		return "", newUpdateError(ErrReplaceFailed, "设置待替换文件权限失败", err)
	}
	backupPath := filepath.Join(backupDirectory, fmt.Sprintf("vohive-%s.bak", m.now().UTC().Format("20060102T150405.000000000Z")))
	if err := os.Rename(executable, backupPath); err != nil {
		return "", newUpdateError(ErrBackupFailed, "备份当前可执行文件失败", err)
	}
	if err := os.Rename(source, executable); err != nil {
		if rollbackErr := os.Rename(backupPath, executable); rollbackErr != nil {
			return "", newUpdateError(ErrRollbackFailed, "替换失败且无法恢复旧版本", rollbackErr)
		}
		return "", newUpdateError(ErrReplaceFailed, "替换当前可执行文件失败", err)
	}
	return backupPath, nil
}

func (m *Manager) MarkStartupHealthy() {
	m.mu.Lock()
	if m.state.State != StateRestarting {
		m.mu.Unlock()
		return
	}
	m.state.State = StateSuccess
	m.state.CurrentVersion = strings.TrimSpace(global.Version)
	m.state.Progress = 100
	m.state.Message = "新版本已启动"
	m.state.Error = ""
	m.state.ErrorCode = ""
	m.state.UpdatedAt = m.now()
	status := m.state
	m.running = false
	m.mu.Unlock()
	m.persistStatus(status)
}

func (m *Manager) RollbackLastUpdate() error {
	m.mu.Lock()
	if m.state.State != StateRestarting || m.state.BackupPath == "" {
		m.mu.Unlock()
		return newUpdateError(ErrRollbackFailed, "没有可回滚的更新", nil)
	}
	backupPath := m.state.BackupPath
	m.mu.Unlock()

	executable, err := m.executable()
	if err != nil {
		return newUpdateError(ErrRollbackFailed, "无法定位当前可执行文件", err)
	}
	failedPath := executable + ".failed"
	if err := os.Rename(executable, failedPath); err != nil {
		return newUpdateError(ErrRollbackFailed, "移动失败版本失败", err)
	}
	if err := os.Rename(backupPath, executable); err != nil {
		_ = os.Rename(failedPath, executable)
		return newUpdateError(ErrRollbackFailed, "恢复旧版本失败", err)
	}
	_ = os.Remove(failedPath)
	m.mu.Lock()
	m.state.State = StateRolledBack
	m.state.Progress = 100
	m.state.Message = "新版本启动失败，已恢复旧版本"
	m.state.Error = "启动健康检查超时"
	m.state.ErrorCode = string(ErrRollbackFailed)
	m.state.UpdatedAt = m.now()
	status := m.state
	m.running = false
	m.mu.Unlock()
	m.persistStatus(status)
	return nil
}

func (m *Manager) armStartupHealthWindow() {
	go func() {
		timer := time.NewTimer(45 * time.Second)
		defer timer.Stop()
		<-timer.C
		if status := m.Status(); status.State == StateRestarting {
			if err := m.RollbackLastUpdate(); err != nil {
				logger.Error("更新启动健康检查回滚失败", "err", err)
			}
		}
	}()
}

func (m *Manager) setState(state UpdateState, progress int, message, target string) {
	m.mu.Lock()
	m.state.State = state
	m.state.Progress = progress
	m.state.Message = message
	m.state.TargetVersion = target
	m.state.Error = ""
	m.state.ErrorCode = ""
	m.state.UpdatedAt = m.now()
	status := m.state
	m.mu.Unlock()
	m.persistStatus(status)
}

func (m *Manager) fail(err error) {
	m.mu.Lock()
	m.state.State = StateFailed
	m.state.Progress = 0
	m.state.Error = err.Error()
	m.state.ErrorCode = ErrorCodeOf(err)
	m.state.Message = "更新失败"
	m.state.UpdatedAt = m.now()
	status := m.state
	m.running = false
	m.mu.Unlock()
	m.persistStatus(status)
	logger.Error("应用更新失败", "code", status.ErrorCode, "err", err)
}

func (m *Manager) rollbackState(err error) {
	m.mu.Lock()
	m.state.State = StateRolledBack
	m.state.Progress = 0
	m.state.Error = err.Error()
	m.state.ErrorCode = ErrorCodeOf(err)
	m.state.Message = "更新失败，旧版本未被破坏"
	m.state.UpdatedAt = m.now()
	status := m.state
	m.running = false
	m.mu.Unlock()
	m.persistStatus(status)
	logger.Warn("更新替换失败，已保留旧版本", "code", status.ErrorCode, "err", err)
}

func (m *Manager) loadPersistedStatus() {
	path, err := m.statusPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var status UpdateStatus
	if err := json.Unmarshal(data, &status); err != nil || status.State == "" {
		return
	}
	m.state = status
}

func (m *Manager) persistStatus(status UpdateStatus) {
	path, err := m.statusPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.Warn("写入更新状态目录失败", "path", path, "err", err)
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".status-*")
	if err != nil {
		logger.Warn("创建更新状态临时文件失败", "path", path, "err", err)
		return
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		os.Remove(temporaryPath)
	}()
	if err := json.NewEncoder(temporary).Encode(status); err != nil {
		logger.Warn("编码更新状态失败", "path", path, "err", err)
		return
	}
	if err := temporary.Chmod(0o600); err != nil {
		logger.Warn("设置更新状态权限失败", "path", path, "err", err)
		return
	}
	if err := temporary.Close(); err != nil {
		logger.Warn("关闭更新状态临时文件失败", "path", path, "err", err)
		return
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		logger.Warn("替换更新状态文件失败", "path", path, "err", err)
	}
}

func (m *Manager) statusPath() (string, error) {
	executable, err := m.executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "update", "status.json"), nil
}

func updateInProgress(state UpdateState) bool {
	switch state {
	case StateChecking, StateAvailable, StateDownloading, StateVerifying, StateBackingUp, StateApplying, StateRestarting:
		return true
	default:
		return false
	}
}
