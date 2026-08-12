package db

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iniwex5/vohive/internal/upstreamproxy"
)

func startDBProxyProbeServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error=%v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				greeting := make([]byte, 3)
				if _, err := io.ReadFull(conn, greeting); err != nil {
					return
				}
				_, _ = conn.Write([]byte{0x05, 0x00})
				request := make([]byte, 10)
				if _, err := io.ReadFull(conn, request); err != nil {
					return
				}
				_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x17, 0x70})
			}(conn)
		}
	}()
	return listener.Addr().String()
}

func openTestDB(t *testing.T) {
	t.Helper()
	if err := Init(filepath.Join(t.TempDir(), "test.db")); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	loadCountryTableFixture(t)
}

func loadCountryTableFixture(t *testing.T) {
	t.Helper()
	cachePath := filepath.Join(t.TempDir(), "mcc-mnc-table.json")
	rows := `[{"mcc":"310","mnc":"260","iso":"us","country":"United States","country_code":"US","network":"T-Mobile"}]`
	if err := os.WriteFile(cachePath, []byte(rows), 0o644); err != nil {
		t.Fatalf("WriteFile() error=%v", err)
	}
	result := upstreamproxy.InitCountryTable(context.Background(), upstreamproxy.CountryTableOptions{
		CachePath: cachePath,
		SourceURL: "http://127.0.0.1:1/missing",
	})
	if result.Err != nil {
		t.Fatalf("InitCountryTable() error=%v", result.Err)
	}
}

func TestUpstreamProxyCountryRuleSelectsEnabledProxyByHomeMCC(t *testing.T) {
	openTestDB(t)
	now := time.Now()
	if err := UpsertUpstreamProxy(UpstreamProxy{ID: "proxy-us", Name: "US", Addr: "127.0.0.1:1080", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: " us ", UpstreamProxyID: "proxy-us", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err := GetHomeMCCUpstreamProxy("310")
	if err != nil {
		t.Fatalf("GetHomeMCCUpstreamProxy() error=%v", err)
	}
	if country != "US" || proxy == nil || proxy.ID != "proxy-us" {
		t.Fatalf("proxy=%+v country=%q, want proxy-us/US", proxy, country)
	}
}

func TestUpstreamProxyCountryRuleDirectWhenNoRuleOrDisabled(t *testing.T) {
	openTestDB(t)
	if err := UpsertUpstreamProxy(UpstreamProxy{ID: "proxy-us", Addr: "127.0.0.1:1080", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxy() error=%v", err)
	}
	proxy, country, err := GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("no rule proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: "US", UpstreamProxyID: "proxy-us", Enabled: false}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err = GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("disabled rule proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
}

func TestUpstreamProxyCountryRuleDirectWhenUnknownMCCOrMissingProxy(t *testing.T) {
	openTestDB(t)
	proxy, country, err := GetHomeMCCUpstreamProxy("404")
	if err != nil || proxy != nil || country != "" {
		t.Fatalf("unknown mcc proxy=%+v country=%q err=%v, want nil/empty/nil", proxy, country, err)
	}
	if err := UpsertUpstreamProxyCountryRule(UpstreamProxyCountryRule{CountryCode: "US", UpstreamProxyID: "missing", Enabled: true}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRule() error=%v", err)
	}
	proxy, country, err = GetHomeMCCUpstreamProxy("310")
	if err != nil || proxy != nil || country != "US" {
		t.Fatalf("missing proxy proxy=%+v country=%q err=%v, want nil/US/nil", proxy, country, err)
	}
}

func TestSelectHomeMCCUpstreamProxyFailsOverToSecondMember(t *testing.T) {
	openTestDB(t)
	goodAddr := startDBProxyProbeServer(t)
	closedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error=%v", err)
	}
	badAddr := closedListener.Addr().String()
	_ = closedListener.Close()
	for _, proxy := range []UpstreamProxy{
		{ID: "bad", Addr: badAddr, Enabled: true},
		{ID: "good", Addr: goodAddr, Enabled: true},
	} {
		if err := UpsertUpstreamProxy(proxy); err != nil {
			t.Fatalf("UpsertUpstreamProxy(%s) error=%v", proxy.ID, err)
		}
	}
	rule := UpstreamProxyCountryRule{CountryCode: "US", Enabled: true, Required: true, AutoFailover: true, PinnedProxyID: "bad"}
	if err := UpsertUpstreamProxyCountryRuleSet(rule, []string{"bad", "good"}); err != nil {
		t.Fatalf("UpsertUpstreamProxyCountryRuleSet() error=%v", err)
	}
	selection, err := SelectHomeMCCUpstreamProxy(context.Background(), "310")
	if err != nil {
		t.Fatalf("SelectHomeMCCUpstreamProxy() error=%v", err)
	}
	if selection.Proxy == nil || selection.Proxy.ID != "good" || len(selection.Attempted) != 2 {
		t.Fatalf("selection=%+v", selection)
	}
	selection, err = SelectHomeMCCUpstreamProxy(context.Background(), "310")
	if err != nil {
		t.Fatalf("second SelectHomeMCCUpstreamProxy() error=%v", err)
	}
	if selection.Proxy == nil || selection.Proxy.ID != "good" || len(selection.Attempted) != 1 {
		t.Fatalf("second selection=%+v, want cooling bad endpoint skipped behind healthy endpoint", selection)
	}
}
