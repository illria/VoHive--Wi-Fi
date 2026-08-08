package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/vohive/internal/global"
)

func setTestVersion(t *testing.T, version string) {
	t.Helper()
	previous := global.Version
	global.Version = version
	t.Cleanup(func() {
		global.Version = previous
	})
}

func testRelease(tag, body string, assets []Asset) Release {
	return Release{
		TagName: tag,
		Name:    tag,
		Body:    body,
		Assets:  assets,
	}
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatalf("encode JSON: %v", err)
	}
}

func newTestManager(t *testing.T, apiBaseURL string, isDocker func() bool, signal func(os.Signal) error) *Manager {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "vohive")
	if err := os.WriteFile(executable, []byte("old-version"), 0o755); err != nil {
		t.Fatalf("create executable: %v", err)
	}
	if isDocker == nil {
		isDocker = func() bool { return false }
	}
	if signal == nil {
		signal = func(os.Signal) error { return nil }
	}
	return NewManager(Options{
		APIBaseURL:    apiBaseURL,
		HTTPClient:    &http.Client{Timeout: 2 * time.Second},
		Executable:   func() (string, error) { return executable, nil },
		Signal:        signal,
		IsDocker:      isDocker,
		MinBinarySize: 1,
		MaxBinarySize: 1 << 20,
		SignalDelay:   time.Millisecond,
	})
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		valid bool
	}{
		{name: "with v", input: "v1.2.3", want: "v1.2.3", valid: true},
		{name: "without v", input: "1.2.3-rc.1", want: "v1.2.3-rc.1", valid: true},
		{name: "invalid", input: "release", valid: false},
		{name: "empty", input: " ", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, valid := normalizeVersion(test.input)
			if got != test.want || valid != test.valid {
				t.Fatalf("normalizeVersion(%q) = %q, %v; want %q, %v", test.input, got, valid, test.want, test.valid)
			}
		})
	}
}

func TestLegacyVersion(t *testing.T) {
	if !isLegacyVersion("portable") || !isLegacyVersion("Unknown") {
		t.Fatal("portable and unknown should be migration versions")
	}
	if isLegacyVersion("v1.0.0") {
		t.Fatal("formal SemVer must not be treated as legacy")
	}
}

func TestAssetKey(t *testing.T) {
	tests := []struct {
		goos, goarch, goarm string
		want                string
		ok                  bool
	}{
		{goos: "linux", goarch: "amd64", want: "amd64", ok: true},
		{goos: "linux", goarch: "arm64", want: "arm64", ok: true},
		{goos: "linux", goarch: "arm", goarm: "7", want: "armv7", ok: true},
		{goos: "linux", goarch: "arm", want: "armv7", ok: true},
		{goos: "darwin", goarch: "arm64", ok: false},
		{goos: "linux", goarch: "386", ok: false},
	}
	for _, test := range tests {
		got, ok := assetKey(test.goos, test.goarch, test.goarm)
		if got != test.want || ok != test.ok {
			t.Errorf("assetKey(%q, %q, %q) = %q, %v; want %q, %v", test.goos, test.goarch, test.goarm, got, ok, test.want, test.ok)
		}
	}
}

func TestSelectPrereleaseRelease(t *testing.T) {
	release, err := selectPrereleaseRelease([]Release{
		{TagName: "v1.4.0", Prerelease: false},
		{TagName: "v1.5.0-rc.1", Prerelease: true},
		{TagName: "v1.5.0-beta.2", Prerelease: true},
		{TagName: "not-a-version", Prerelease: true},
	})
	if err != nil {
		t.Fatalf("selectPrereleaseRelease returned error: %v", err)
	}
	if release.TagName != "v1.5.0-rc.1" {
		t.Fatalf("selected tag = %q, want v1.5.0-rc.1", release.TagName)
	}
	if _, err := selectPrereleaseRelease([]Release{{TagName: "v1.0.0"}}); ErrorCodeOf(err) != string(ErrReleaseNotFound) {
		t.Fatalf("expected release_not_found for empty prerelease set, got %v", err)
	}
}

func TestFindAssets(t *testing.T) {
	binary, checksum, err := findAssets([]Asset{
		{Name: "vohive_v1.2.3_linux_arm64", BrowserDownloadURL: "binary"},
		{Name: "vohive_v1.2.3_linux_arm64.sha256", BrowserDownloadURL: "checksum"},
	}, "vohive_v1.2.3_linux_arm64")
	if err != nil || binary.BrowserDownloadURL != "binary" || checksum.BrowserDownloadURL != "checksum" {
		t.Fatalf("findAssets returned %v, %v, %v", binary, checksum, err)
	}
	if _, _, err := findAssets([]Asset{{Name: "other"}}, "vohive_v1.2.3_linux_arm64"); ErrorCodeOf(err) != string(ErrAssetNotFound) {
		t.Fatalf("expected asset_not_found, got %v", err)
	}
	if _, _, err := findAssets([]Asset{{Name: "vohive_v1.2.3_linux_arm64", BrowserDownloadURL: "binary"}}, "vohive_v1.2.3_linux_arm64"); ErrorCodeOf(err) != string(ErrChecksumAssetNotFound) {
		t.Fatalf("expected checksum_asset_not_found, got %v", err)
	}
}

func TestCheckUpdateUsesNewRepositoryAndSemver(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/illria/VoHive--Wi-Fi/releases/latest" {
			t.Errorf("unexpected update path: %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(t, writer, testRelease("v1.2.0", "安全修复", nil))
	}))
	defer server.Close()

	manager := newTestManager(t, server.URL, nil, nil)
	info, err := manager.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate returned error: %v", err)
	}
	if !info.HasUpdate || info.LatestVer != "v1.2.0" || info.Channel != string(ChannelStable) {
		t.Fatalf("unexpected update info: %+v", info)
	}
}

func TestCheckUpdateRejectsInvalidCurrentVersion(t *testing.T) {
	setTestVersion(t, "not-semver")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, testRelease("v1.2.0", "", nil))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	if _, err := manager.CheckUpdate(); ErrorCodeOf(err) != string(ErrInvalidCurrentVersion) {
		t.Fatalf("expected invalid_current_version, got %v", err)
	}
}

func TestCheckUpdateMarksLegacyMigration(t *testing.T) {
	setTestVersion(t, "portable")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, testRelease("v1.2.0", "", nil))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	info, err := manager.CheckUpdate()
	if err != nil || !info.HasUpdate || !info.MigrationRequired {
		t.Fatalf("unexpected migration info: %+v, error=%v", info, err)
	}
}

func TestCheckUpdateReportsNoUpdate(t *testing.T) {
	setTestVersion(t, "v2.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, testRelease("v1.2.0", "", nil))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	info, err := manager.CheckUpdate()
	if err != nil || info.HasUpdate {
		t.Fatalf("expected no update, info=%+v error=%v", info, err)
	}
}

func TestPrereleaseChannel(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/illria/VoHive--Wi-Fi/releases" {
			t.Errorf("unexpected prerelease path: %s", request.URL.Path)
		}
		writeJSON(t, writer, []Release{
			{TagName: "v1.1.0-beta.1", Prerelease: true},
			{TagName: "v1.0.1", Prerelease: false},
		})
	}))
	defer server.Close()
	manager := NewManager(Options{
		APIBaseURL:  server.URL,
		Channel:     ChannelPrerelease,
		HTTPClient:  server.Client(),
		IsDocker:    func() bool { return false },
		Executable:  func() (string, error) { return filepath.Join(t.TempDir(), "vohive"), nil },
	})
	info, err := manager.CheckUpdate()
	if err != nil || info.LatestVer != "v1.1.0-beta.1" || info.Channel != string(ChannelPrerelease) {
		t.Fatalf("unexpected prerelease info: %+v, error=%v", info, err)
	}
}

func TestDownloadBinaryWritesAndEnforcesSize(t *testing.T) {
	payload := bytesForTest(4096)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	path, err := manager.downloadBinary(Asset{BrowserDownloadURL: server.URL}, t.TempDir(), "v1.2.0")
	if err != nil {
		t.Fatalf("downloadBinary returned error: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload differs: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestDownloadRejectsHTTPTimeoutAndEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/http-error":
			writer.WriteHeader(http.StatusBadGateway)
		case "/empty":
			return
		case "/slow":
			time.Sleep(50 * time.Millisecond)
			_, _ = io.WriteString(writer, "late response")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	if _, err := manager.downloadBinary(Asset{BrowserDownloadURL: server.URL + "/http-error"}, t.TempDir(), "v1.2.0"); ErrorCodeOf(err) != string(ErrDownloadFailed) {
		t.Fatalf("expected download_failed for HTTP error, got %v", err)
	}
	if _, err := manager.downloadBinary(Asset{BrowserDownloadURL: server.URL + "/empty"}, t.TempDir(), "v1.2.0"); ErrorCodeOf(err) != string(ErrDownloadFailed) {
		t.Fatalf("expected download_failed for empty file, got %v", err)
	}
	manager.httpClient = &http.Client{Timeout: time.Millisecond}
	manager.downloadHTTPClient = &http.Client{Timeout: time.Millisecond}
	if _, err := manager.downloadBinary(Asset{BrowserDownloadURL: server.URL + "/slow"}, t.TempDir(), "v1.2.0"); ErrorCodeOf(err) != string(ErrDownloadFailed) {
		t.Fatalf("expected download_failed for timeout, got %v", err)
	}
}

func TestDownloadBinaryUsesSeparateDownloadTimeout(t *testing.T) {
	payload := bytesForTest(4096)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = writer.Write(payload)
	}))
	defer server.Close()

	manager := newTestManager(t, server.URL, nil, nil)
	manager.httpClient = &http.Client{Timeout: 10 * time.Millisecond}
	manager.downloadHTTPClient = &http.Client{Timeout: time.Second}
	path, err := manager.downloadBinary(Asset{BrowserDownloadURL: server.URL}, t.TempDir(), "v1.2.0")
	if err != nil {
		t.Fatalf("downloadBinary should use the separate download client: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded payload differs: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestDownloadChecksumAndVerify(t *testing.T) {
	payload := []byte("checksum-payload")
	digest := sha256.Sum256(payload)
	checksumText := hex.EncodeToString(digest[:]) + "  vohive_v1.2.0_linux_amd64\n"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, checksumText)
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, nil)
	checksum, err := manager.downloadChecksum(Asset{BrowserDownloadURL: server.URL})
	if err != nil || checksum != hex.EncodeToString(digest[:]) {
		t.Fatalf("downloadChecksum = %q, error=%v", checksum, err)
	}
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(path, checksum); err != nil {
		t.Fatalf("verifyChecksum returned error: %v", err)
	}
	if err := verifyChecksum(path, strings.Repeat("0", sha256.Size*2)); ErrorCodeOf(err) != string(ErrChecksumMismatch) {
		t.Fatalf("expected checksum_mismatch, got %v", err)
	}
}

func TestReplaceAndRollback(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "vohive")
	source := filepath.Join(root, "download")
	backupDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{
		Executable: func() (string, error) { return executable, nil },
		Now:        func() time.Time { return time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC) },
		IsDocker:   func() bool { return false },
	})
	backupPath, err := manager.replaceBinary(source, executable, backupDir)
	if err != nil {
		t.Fatalf("replaceBinary returned error: %v", err)
	}
	if got, _ := os.ReadFile(executable); string(got) != "new-version" {
		t.Fatalf("executable was not replaced: %q", got)
	}
	manager.mu.Lock()
	manager.state.State = StateRestarting
	manager.state.BackupPath = backupPath
	manager.mu.Unlock()
	if err := manager.RollbackLastUpdate(); err != nil {
		t.Fatalf("RollbackLastUpdate returned error: %v", err)
	}
	if got, _ := os.ReadFile(executable); string(got) != "old-version" {
		t.Fatalf("rollback did not restore old executable: %q", got)
	}
}

func TestBackupFailureAndReplaceFailureRestore(t *testing.T) {
	root := t.TempDir()
	backupDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	missingExecutable := filepath.Join(root, "missing-vohive")
	manager := NewManager(Options{
		Executable: func() (string, error) { return missingExecutable, nil },
		IsDocker:   func() bool { return false },
	})
	source := filepath.Join(root, "download")
	if err := os.WriteFile(source, []byte("new-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.replaceBinary(source, missingExecutable, backupDir); ErrorCodeOf(err) != string(ErrBackupFailed) {
		t.Fatalf("expected backup_failed, got %v", err)
	}

	executable := filepath.Join(root, "vohive")
	if err := os.WriteFile(executable, []byte("old-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	missingSource := filepath.Join(root, "missing-download")
	if _, err := manager.replaceBinary(missingSource, executable, backupDir); ErrorCodeOf(err) != string(ErrReplaceFailed) {
		t.Fatalf("expected replace_failed, got %v", err)
	}
	if got, err := os.ReadFile(executable); err != nil || string(got) != "old-version" {
		t.Fatalf("replace failure did not restore old executable: %q, error=%v", got, err)
	}
}

func TestUpdateStateFlow(t *testing.T) {
	manager := newTestManager(t, "http://127.0.0.1:1", nil, nil)
	manager.setState(StateChecking, 0, "正在检查更新", "")
	manager.setState(StateDownloading, 43, "正在下载更新", "v1.0.1")
	manager.setState(StateVerifying, 43, "正在验证 SHA-256", "v1.0.1")
	manager.setState(StateApplying, 80, "正在替换当前版本", "v1.0.1")
	manager.setState(StateRestarting, 100, "更新已应用，等待服务重启", "v1.0.1")
	status := manager.Status()
	if status.State != StateRestarting || status.TargetVersion != "v1.0.1" || status.Progress != 100 {
		t.Fatalf("unexpected final state: %+v", status)
	}
}

func TestStartUpdateRejectsBusyAndDocker(t *testing.T) {
	manager := newTestManager(t, "http://127.0.0.1:1", nil, nil)
	manager.mu.Lock()
	manager.running = true
	manager.mu.Unlock()
	if _, err := manager.StartUpdate(); ErrorCodeOf(err) != string(ErrUpdateInProgress) {
		t.Fatalf("expected update_in_progress, got %v", err)
	}

	dockerManager := newTestManager(t, "http://127.0.0.1:1", func() bool { return true }, nil)
	if _, err := dockerManager.StartUpdate(); ErrorCodeOf(err) != string(ErrDockerUnsupported) {
		t.Fatalf("expected docker_update_unsupported, got %v", err)
	}
}

func TestRunUpdateDownloadsVerifiesReplacesAndSignals(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	if runtime.GOOS != "linux" {
		t.Skip("release assets are Linux-only")
	}
	arch, supported := runtimeAssetKey()
	if !supported {
		t.Skip("test runner architecture is not a supported release target")
	}
	payload := bytesForTest(4096)
	digest := sha256.Sum256(payload)
	signaled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/illria/VoHive--Wi-Fi/releases/latest":
			writeJSON(t, writer, testRelease("v1.1.0", "update", []Asset{
				{Name: "vohive_v1.1.0_linux_" + arch, BrowserDownloadURL: serverURL(request, "binary")},
				{Name: "vohive_v1.1.0_linux_" + arch + ".sha256", BrowserDownloadURL: serverURL(request, "checksum")},
			}))
		case "/binary":
			_, _ = writer.Write(payload)
		case "/checksum":
			_, _ = io.WriteString(writer, hex.EncodeToString(digest[:])+"  binary\n")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL, nil, func(os.Signal) error {
		select {
		case signaled <- struct{}{}:
		default:
		}
		return nil
	})
	_, err := manager.StartUpdate()
	if err != nil {
		t.Fatalf("StartUpdate returned error: %v", err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		status := manager.Status()
		if status.State == StateRestarting {
			if status.BackupPath == "" {
				t.Fatalf("restart status did not retain backup path: %+v", status)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for update, status=%+v", status)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	signalDeadline := time.NewTimer(3 * time.Second)
	defer signalDeadline.Stop()
	select {
	case <-signaled:
	case <-signalDeadline.C:
		t.Fatal("update did not signal restart")
	}
}

func TestRunUpdatePinsProxySelectedDuringAutoCheck(t *testing.T) {
	setTestVersion(t, "v1.0.0")
	if runtime.GOOS != "linux" {
		t.Skip("release assets are Linux-only")
	}
	arch, supported := runtimeAssetKey()
	if !supported {
		t.Skip("test runner architecture is not a supported release target")
	}

	payload := bytesForTest(4096)
	digest := sha256.Sum256(payload)
	binaryURL := "https://github.com/illria/VoHive--Wi-Fi/releases/download/v1.1.0/vohive_v1.1.0_linux_" + arch
	checksumURL := binaryURL + ".sha256"
	releaseBody, err := json.Marshal(testRelease("v1.1.0", "update", []Asset{
		{Name: "vohive_v1.1.0_linux_" + arch, BrowserDownloadURL: binaryURL},
		{Name: "vohive_v1.1.0_linux_" + arch + ".sha256", BrowserDownloadURL: checksumURL},
	}))
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}

	var requestedURLs []string
	response := func(request *http.Request, status int, body []byte) *http.Response {
		return &http.Response{
			StatusCode:    status,
			Status:        http.StatusText(status),
			Header:        make(http.Header),
			Body:          io.NopCloser(bytes.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       request,
		}
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestURL := request.URL.String()
		requestedURLs = append(requestedURLs, requestURL)
		switch {
		case strings.HasPrefix(requestURL, "https://ghfast.top/"):
			return response(request, http.StatusGatewayTimeout, []byte("ghfast unavailable")), nil
		case strings.HasPrefix(requestURL, "https://ghproxy.net/") && strings.Contains(requestURL, "/releases/latest"):
			return response(request, http.StatusOK, releaseBody), nil
		case strings.HasPrefix(requestURL, "https://ghproxy.net/") && strings.Contains(requestURL, "/releases/download/"):
			if strings.HasSuffix(requestURL, ".sha256") {
				return response(request, http.StatusOK, []byte(hex.EncodeToString(digest[:])+"  binary\n")), nil
			}
			return response(request, http.StatusOK, payload), nil
		default:
			return response(request, http.StatusNotFound, nil), nil
		}
	})

	root := t.TempDir()
	executable := filepath.Join(root, "vohive")
	if err := os.WriteFile(executable, []byte("old-version"), 0o755); err != nil {
		t.Fatalf("create executable: %v", err)
	}
	signaled := make(chan struct{}, 1)
	manager := NewManager(Options{
		HTTPClient:    &http.Client{Transport: transport, Timeout: 2 * time.Second},
		Executable:   func() (string, error) { return executable, nil },
		Signal:        func(os.Signal) error { signaled <- struct{}{}; return nil },
		IsDocker:      func() bool { return false },
		MinBinarySize: 1,
		MaxBinarySize: 1 << 20,
		SignalDelay:   time.Millisecond,
	})

	manager.runUpdate(ProxyAuto, "")
	if status := manager.Status(); status.State != StateRestarting {
		t.Fatalf("update did not reach restarting state: %+v", status)
	}
	select {
	case <-signaled:
	case <-time.After(time.Second):
		t.Fatal("update did not signal restart")
	}

	for _, requestURL := range requestedURLs {
		if strings.Contains(requestURL, "/releases/download/") && !strings.HasPrefix(requestURL, "https://ghproxy.net/") {
			t.Fatalf("asset request switched away from selected proxy: %s", requestURL)
		}
	}
	if len(requestedURLs) != 4 {
		t.Fatalf("unexpected request sequence: %+v", requestedURLs)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func bytesForTest(size int) []byte {
	payload := make([]byte, size)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	return payload
}

func serverURL(request *http.Request, path string) string {
	return "http://" + request.Host + "/" + path
}
