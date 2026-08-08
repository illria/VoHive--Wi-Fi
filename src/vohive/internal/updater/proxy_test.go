package updater

import "testing"

func TestGitHubProxyOptionsExposeAutoDirectAndCustom(t *testing.T) {
	options := GitHubProxyOptions()
	if len(options) < 3 {
		t.Fatalf("expected multiple GitHub update entries, got %d", len(options))
	}
	if options[0].ID != ProxyAuto {
		t.Fatalf("first option = %q, want %q", options[0].ID, ProxyAuto)
	}
	foundDirect := false
	foundCustom := false
	for _, option := range options {
		if option.ID == ProxyDirect {
			foundDirect = true
		}
		if option.ID == ProxyCustom {
			foundCustom = true
		}
		if option.ID == "" || option.Name == "" || option.Description == "" {
			t.Fatalf("incomplete proxy option: %+v", option)
		}
	}
	if !foundDirect {
		t.Fatal("direct GitHub option is missing")
	}
	if !foundCustom {
		t.Fatal("custom GitHub option is missing")
	}
}

func TestProxyCandidates(t *testing.T) {
	auto, ok := proxyCandidates(ProxyAuto)
	if !ok || len(auto) < 3 {
		t.Fatalf("auto candidates = %d, %v", len(auto), ok)
	}
	selected, ok := proxyCandidates("ghfast")
	if !ok || len(selected) != 1 || selected[0].ID != "ghfast" {
		t.Fatalf("selected candidates = %+v, %v", selected, ok)
	}
	if _, ok := proxyCandidates("unknown"); ok {
		t.Fatal("unknown proxy should be rejected")
	}
}

func TestRewriteGitHubURL(t *testing.T) {
	proxy, ok := proxyByID("ghfast")
	if !ok {
		t.Fatal("ghfast proxy is missing")
	}
	if got, want := rewriteGitHubURL(proxy, "https://api.github.com/repos/illria/VoHive--Wi-Fi/releases/latest"), "https://ghfast.top/https://api.github.com/repos/illria/VoHive--Wi-Fi/releases/latest"; got != want {
		t.Fatalf("rewritten API URL = %q, want %q", got, want)
	}
	if got, want := rewriteGitHubURL(proxy, "https://github.com/illria/VoHive--Wi-Fi/releases/download/v1.0.3/file.tar.gz"), "https://ghfast.top/https://github.com/illria/VoHive--Wi-Fi/releases/download/v1.0.3/file.tar.gz"; got != want {
		t.Fatalf("rewritten asset URL = %q, want %q", got, want)
	}
	if got := rewriteGitHubURL(proxy, "http://127.0.0.1:8080/releases/latest"); got != "http://127.0.0.1:8080/releases/latest" {
		t.Fatalf("non-GitHub URL should not be rewritten: %q", got)
	}
}

func TestCustomProxyURL(t *testing.T) {
	candidates, err := proxyCandidatesWithURL(ProxyCustom, " https://proxy.example.com/github/// ")
	if err != nil {
		t.Fatalf("custom proxy should be accepted: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("custom proxy candidates = %d, want 1", len(candidates))
	}
	proxy := candidates[0]
	if proxy.prefix != "https://proxy.example.com/github/" {
		t.Fatalf("custom proxy prefix = %q", proxy.prefix)
	}
	want := "https://proxy.example.com/github/https://api.github.com/repos/illria/VoHive--Wi-Fi/releases/latest"
	if got := rewriteGitHubURL(proxy, "https://api.github.com/repos/illria/VoHive--Wi-Fi/releases/latest"); got != want {
		t.Fatalf("custom API URL = %q, want %q", got, want)
	}

	for _, invalid := range []string{
		"",
		"github.com/proxy",
		"ftp://proxy.example.com/",
		"https://proxy.example.com/?token=secret",
		"https://user:pass@proxy.example.com/",
	} {
		if _, err := proxyCandidatesWithURL(ProxyCustom, invalid); err == nil {
			t.Fatalf("custom proxy %q should be rejected", invalid)
		}
	}
}
