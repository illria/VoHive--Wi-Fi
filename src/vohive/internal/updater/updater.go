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
	"strconv"
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
	// Release metadata should fail fast when an endpoint is unavailable, but a
	// large binary may legitimately take longer than that to arrive through a
	// public proxy. The download request has no whole-body client timeout; this
	// is the per-attempt upper bound, while idleTimeoutReader handles a stalled
	// body without interrupting an otherwise slow but active transfer.
	defaultDownloadTimeout       = 30 * time.Minute
	defaultDownloadIdleTimeout   = 60 * time.Second
	defaultDownloadRetryAttempts = 4
	defaultDownloadRetryDelay    = 500 * time.Millisecond
	defaultChecksumTimeout       = 2 * time.Minute
	defaultChecksumRetryAttempts = 3
	defaultChecksumRetryDelay    = 500 * time.Millisecond
	defaultSignalDelay           = 2 * time.Second
	defaultMinimumFileSize       = 64 * 1024
	defaultMaximumFileSize       = 128 * 1024 * 1024
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
	HTTPClient         *http.Client
	DownloadHTTPClient *http.Client
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
	httpClient         *http.Client
	downloadHTTPClient *http.Client
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
	if options.DownloadHTTPClient == nil {
		// Preserve the caller's transport, redirect policy and cookie jar while
		// removing the short metadata deadline from binary body reads. A total
		// http.Client timeout aborts slow transfers even when bytes keep arriving.
		downloadClient := *options.HTTPClient
		downloadClient.Timeout = 0
		options.DownloadHTTPClient = &downloadClient
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
		httpClient:         options.HTTPClient,
		downloadHTTPClient: options.DownloadHTTPClient,
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

func CheckUpdateWithProxyURL(proxyID, customProxyURL string) (*UpdateInfo, error) {
	return defaultManager.CheckUpdateWithProxyURL(proxyID, customProxyURL)
}

func ApplyUpdate() error {
	_, err := defaultManager.StartUpdate()
	return err
}

func StartUpdate() (UpdateStatus, error) { return defaultManager.StartUpdate() }

func StartUpdateWithProxy(proxyID string) (UpdateStatus, error) {
	return defaultManager.StartUpdateWithProxy(proxyID)
}

func StartUpdateWithProxyURL(proxyID, customProxyURL string) (UpdateStatus, error) {
	return defaultManager.StartUpdateWithProxyURL(proxyID, customProxyURL)
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
	return m.CheckUpdateWithProxyURL(proxyID, "")
}

func (m *Manager) CheckUpdateWithProxyURL(proxyID, customProxyURL string) (*UpdateInfo, error) {
	release, proxy, err := m.fetchReleaseWithProxyURL(proxyID, customProxyURL)
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
	return m.StartUpdateWithProxyURL(proxyID, "")
}

func (m *Manager) StartUpdateWithProxyURL(proxyID, customProxyURL string) (UpdateStatus, error) {
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

	go m.runUpdate(proxyID, customProxyURL)
	return status, nil
}

func (m *Manager) runUpdate(proxyID, customProxyURL string) {
	release, proxy, err := m.fetchReleaseWithProxyURL(proxyID, customProxyURL)
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

	// The metadata request may use auto mode to find a working entry. Keep that
	// concrete entry for the entire binary download; rotating after a response
	// body has started can leave a partial binary and make the next endpoint
	// unable to resume the same transfer. Once the binary is complete, the small
	// checksum file may safely use the remaining auto-mode entries if needed.
	checksumProxies := checksumProxyCandidates(proxyID, customProxyURL, proxy)
	downloadPath, err := m.downloadAndVerifyAssetsWithChecksumProxies(binaryAsset, checksumAsset, downloadDir, latestVersion, proxy, checksumProxies)
	if err != nil {
		m.fail(err)
		return
	}
	defer os.Remove(downloadPath)

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
	return m.fetchReleaseWithProxyURL(proxyID, "")
}

func (m *Manager) fetchReleaseWithProxyURL(proxyID, customProxyURL string) (Release, githubProxy, error) {
	candidates, err := proxyCandidatesWithURL(proxyID, customProxyURL)
	if err != nil {
		return Release{}, githubProxy{}, err
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

func (m *Manager) downloadAndVerifyAssets(binaryAsset, checksumAsset Asset, directory, targetVersion string, proxy githubProxy) (string, error) {
	return m.downloadAndVerifyAssetsWithChecksumProxies(binaryAsset, checksumAsset, directory, targetVersion, proxy, []githubProxy{proxy})
}

func (m *Manager) downloadAndVerifyAssetsWithChecksumProxies(binaryAsset, checksumAsset Asset, directory, targetVersion string, proxy githubProxy, checksumProxies []githubProxy) (string, error) {
	if proxy.ID == "" {
		return "", newUpdateError(ErrInvalidGitHubProxy, "未知的 GitHub 加速入口", nil)
	}

	m.setState(StateDownloading, 0, fmt.Sprintf("正在通过 %s 下载更新", proxy.Name), targetVersion)
	downloadPath, err := m.downloadBinaryWithProxy(binaryAsset, directory, targetVersion, proxy)
	if err != nil {
		logger.Warn("GitHub 更新资产下载失败，本次任务保持当前入口", "proxy", proxy.ID, "err", err)
		return "", err
	}

	checksum, checksumProxy, err := m.downloadChecksumFromProxies(checksumAsset, checksumProxies, targetVersion)
	if err == nil {
		err = verifyChecksum(downloadPath, checksum)
	}
	if err != nil {
		_ = os.Remove(downloadPath)
		logger.Warn("GitHub 更新资产校验失败，本次任务保持当前版本", "proxy", proxy.ID, "err", err)
		return "", err
	}

	logger.Debug("GitHub 更新资产 SHA-256 校验入口", "proxy", checksumProxy.ID)
	m.setState(StateVerifying, 100, "SHA-256 校验通过", targetVersion)
	return downloadPath, nil
}

func (m *Manager) downloadBinary(asset Asset, directory, targetVersion string) (string, error) {
	return m.downloadBinaryWithProxy(asset, directory, targetVersion, githubProxy{})
}

func (m *Manager) downloadBinaryWithProxy(asset Asset, directory, targetVersion string, proxy githubProxy) (string, error) {
	if asset.Size > m.maxBinarySize {
		return "", newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
	}

	temporary, err := os.CreateTemp(directory, ".vohive-update-*")
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "创建二进制临时文件失败", err)
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(path)
		return "", newUpdateError(ErrDownloadFailed, "关闭二进制临时文件失败", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(path)
		}
	}()

	var lastErr error
	for attempt := 1; attempt <= defaultDownloadRetryAttempts; attempt++ {
		offset, statErr := fileSize(path)
		if statErr != nil {
			return "", newUpdateError(ErrDownloadFailed, "读取二进制临时文件状态失败", statErr)
		}
		if offset > m.maxBinarySize {
			return "", newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
		}

		err := m.downloadBinaryAttempt(asset, path, offset, targetVersion, proxy)
		if err == nil {
			info, statErr := os.Stat(path)
			if statErr != nil {
				return "", newUpdateError(ErrDownloadFailed, "读取已下载二进制状态失败", statErr)
			}
			if info.Size() > m.maxBinarySize {
				return "", newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
			}
			if asset.Size > 0 && info.Size() != asset.Size {
				lastErr = newUpdateError(ErrDownloadFailed, "二进制下载不完整", io.ErrUnexpectedEOF)
			} else if info.Size() < m.minBinarySize {
				lastErr = newUpdateError(ErrDownloadFailed, "下载文件过小，拒绝替换当前程序", nil)
			} else if err := os.Chmod(path, 0o755); err != nil {
				lastErr = newUpdateError(ErrDownloadFailed, "设置更新文件权限失败", err)
			} else {
				file, openErr := os.OpenFile(path, os.O_WRONLY, 0)
				if openErr != nil {
					lastErr = newUpdateError(ErrDownloadFailed, "打开已下载二进制失败", openErr)
				} else {
					syncErr := file.Sync()
					closeErr := file.Close()
					if syncErr != nil {
						lastErr = newUpdateError(ErrDownloadFailed, "同步更新文件失败", syncErr)
					} else if closeErr != nil {
						lastErr = newUpdateError(ErrDownloadFailed, "关闭更新文件失败", closeErr)
					} else {
						removeTemporary = false
						return path, nil
					}
				}
			}
		} else {
			lastErr = err
		}

		if attempt == defaultDownloadRetryAttempts {
			break
		}
		progress := 0
		if size, sizeErr := fileSize(path); sizeErr == nil && asset.Size > 0 {
			progress = int((size * 100) / asset.Size)
			if progress > 99 {
				progress = 99
			}
		}
		m.setState(StateDownloading, progress,
			fmt.Sprintf("下载中断，正在保留进度重试（%d/%d）", attempt, defaultDownloadRetryAttempts), targetVersion)
		time.Sleep(defaultDownloadRetryDelay)
	}
	if lastErr == nil {
		lastErr = newUpdateError(ErrDownloadFailed, "下载二进制失败", nil)
	}
	return "", lastErr
}

// downloadBinaryAttempt downloads one response into path. A failed body read
// intentionally leaves the partial file in place so the next attempt can use
// HTTP Range against the same pinned GitHub entry.
func (m *Manager) downloadBinaryAttempt(asset Asset, path string, offset int64, targetVersion string, proxy githubProxy) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDownloadTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rewriteGitHubURL(proxy, asset.BrowserDownloadURL), nil)
	if err != nil {
		return newUpdateError(ErrDownloadFailed, "创建二进制下载请求失败", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "VoHive-Updater")
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	response, err := m.downloadHTTPClient.Do(request)
	if err != nil {
		return newUpdateError(ErrDownloadFailed, "下载二进制失败", err)
	}
	defer response.Body.Close()

	startOffset := offset
	total := asset.Size
	if offset > 0 {
		switch response.StatusCode {
		case http.StatusPartialContent:
			start, rangeTotal, ok := parseContentRange(response.Header.Get("Content-Range"))
			if response.Header.Get("Content-Range") != "" && (!ok || start != offset) {
				return newUpdateError(ErrDownloadFailed, "断点续传返回的 Content-Range 无效", nil)
			}
			if rangeTotal > 0 {
				total = rangeTotal
			}
		case http.StatusOK:
			// The endpoint ignored Range. Restart this response from byte zero;
			// do not append a second copy of the binary.
			startOffset = 0
		default:
			return newUpdateError(ErrDownloadFailed, fmt.Sprintf("断点续传返回 HTTP %d", response.StatusCode), nil)
		}
	} else if response.StatusCode != http.StatusOK {
		return newUpdateError(ErrDownloadFailed, fmt.Sprintf("二进制下载返回 HTTP %d", response.StatusCode), nil)
	}

	if total <= 0 && response.ContentLength > 0 {
		total = startOffset + response.ContentLength
	}
	if total > m.maxBinarySize || (response.ContentLength > 0 && startOffset+response.ContentLength > m.maxBinarySize) {
		return newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return newUpdateError(ErrDownloadFailed, "打开二进制临时文件失败", err)
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()
	if err := file.Truncate(startOffset); err != nil {
		return newUpdateError(ErrDownloadFailed, "准备二进制临时文件失败", err)
	}
	if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
		return newUpdateError(ErrDownloadFailed, "定位二进制临时文件失败", err)
	}

	writer := &progressWriter{
		manager: m,
		writer:  file,
		total:   total,
		target:  targetVersion,
		written: startOffset,
	}
	reader := &idleTimeoutReader{body: response.Body, timeout: defaultDownloadIdleTimeout}
	_, copyErr := io.Copy(writer, io.LimitReader(reader, m.maxBinarySize-startOffset+1))
	if err := file.Sync(); err != nil && copyErr == nil {
		copyErr = err
	}
	if err := file.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	closeFile = false
	if copyErr != nil {
		return newUpdateError(ErrDownloadFailed, "写入二进制临时文件失败", copyErr)
	}

	info, err := os.Stat(path)
	if err != nil {
		return newUpdateError(ErrDownloadFailed, "读取已下载二进制状态失败", err)
	}
	if info.Size() > m.maxBinarySize {
		return newUpdateError(ErrDownloadFailed, "二进制超过允许的最大下载大小", nil)
	}
	if total > 0 && info.Size() < total {
		return newUpdateError(ErrDownloadFailed, "二进制下载不完整", io.ErrUnexpectedEOF)
	}
	return nil
}

type idleTimeoutReader struct {
	body    io.ReadCloser
	timeout time.Duration
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	type readResult struct {
		n   int
		err error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		n, err := r.body.Read(p)
		resultCh <- readResult{n: n, err: err}
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	select {
	case result := <-resultCh:
		return result.n, result.err
	case <-timer.C:
		_ = r.body.Close()
		return 0, context.DeadlineExceeded
	}
}

func (r *idleTimeoutReader) Close() error { return r.body.Close() }

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func parseContentRange(value string) (start, total int64, ok bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return 0, 0, false
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes "), "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	span := strings.SplitN(parts[0], "-", 2)
	if len(span) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(span[0]), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	totalText := strings.TrimSpace(parts[1])
	if totalText == "*" {
		return start, 0, true
	}
	total, err = strconv.ParseInt(totalText, 10, 64)
	if err != nil || total <= 0 {
		return 0, 0, false
	}
	return start, total, true
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
	var lastErr error
	for attempt := 1; attempt <= defaultChecksumRetryAttempts; attempt++ {
		checksum, err := m.downloadChecksumAttempt(asset, proxy)
		if err == nil {
			return checksum, nil
		}
		lastErr = err
		// A missing checksum asset is deterministic and should not be retried.
		if ErrorCodeOf(err) == string(ErrChecksumAssetNotFound) || attempt == defaultChecksumRetryAttempts {
			break
		}
		time.Sleep(defaultChecksumRetryDelay)
	}
	return "", lastErr
}

func (m *Manager) downloadChecksumAttempt(asset Asset, proxy githubProxy) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultChecksumTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rewriteGitHubURL(proxy, asset.BrowserDownloadURL), nil)
	if err != nil {
		return "", newUpdateError(ErrDownloadFailed, "创建 SHA-256 下载请求失败", err)
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "VoHive-Updater")
	response, err := m.downloadHTTPClient.Do(request)
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
	reader := &idleTimeoutReader{body: response.Body, timeout: defaultDownloadIdleTimeout}
	data, err := io.ReadAll(io.LimitReader(reader, 64*1024))
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

func (m *Manager) downloadChecksumFromProxies(asset Asset, proxies []githubProxy, targetVersion string) (string, githubProxy, error) {
	if len(proxies) == 0 {
		return "", githubProxy{}, newUpdateError(ErrInvalidGitHubProxy, "没有可用的 SHA-256 校验入口", nil)
	}
	var lastErr error
	for index, proxy := range proxies {
		m.setState(StateVerifying, 0, fmt.Sprintf("正在通过 %s 验证 SHA-256", proxy.Name), targetVersion)
		checksum, err := m.downloadChecksumWithProxy(asset, proxy)
		if err == nil {
			return checksum, proxy, nil
		}
		lastErr = err
		if index+1 < len(proxies) {
			logger.Warn("SHA-256 校验入口不可用，尝试下一个入口（不切换已完成的二进制下载）", "proxy", proxy.ID, "err", err)
		}
	}
	return "", githubProxy{}, lastErr
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
