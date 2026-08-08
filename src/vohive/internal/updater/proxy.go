package updater

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ProxyOption is the safe, user-facing description of a GitHub endpoint.
type ProxyOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type githubProxy struct {
	ProxyOption
	prefix     string
	socks5Addr string
}

const (
	ProxyAuto   = "auto"
	ProxyDirect = "direct"
	ProxyCustom = "custom"
)

const customProxyName = "自定义加速地址"

var (
	errEmptyCustomProxyURL       = errors.New("地址不能为空")
	errCustomProxyURLControlChar = errors.New("地址不能包含换行符")
	errCustomProxyURLSyntax      = errors.New("地址格式无效")
	errCustomProxyURLScheme      = errors.New("地址必须使用 http、https 或 socks5")
	errCustomProxyURLHost        = errors.New("地址必须包含主机名")
	errCustomProxyURLPort        = errors.New("SOCKS5 地址必须包含有效端口")
	errCustomProxyURLShape       = errors.New("地址不能包含用户名、查询参数或片段")
)

func normalizeProxyID(proxyID string) string {
	proxyID = strings.ToLower(strings.TrimSpace(proxyID))
	if proxyID == "" {
		return ProxyAuto
	}
	return proxyID
}

// Public proxy services are intentionally fallback options, not mandatory
// dependencies. Auto mode tries each one while resolving release metadata,
// then uses the first successful entry for assets and only falls back after a
// completed asset request fails or stays below the minimum download rate.
var githubProxyCatalog = []githubProxy{
	{
		ProxyOption: ProxyOption{ID: "ghfast", Name: "ghfast.top", Description: "公共加速入口，适合 GitHub API 和 Release 下载"},
		prefix:      "https://ghfast.top/",
	},
	{
		ProxyOption: ProxyOption{ID: "ghproxy-net", Name: "ghproxy.net", Description: "公共 GitHub 加速入口"},
		prefix:      "https://ghproxy.net/",
	},
	{
		ProxyOption: ProxyOption{ID: "gh-proxy", Name: "gh-proxy.com", Description: "公共 GitHub 加速入口"},
		prefix:      "https://gh-proxy.com/",
	},
	{
		ProxyOption: ProxyOption{ID: ProxyDirect, Name: "直连 GitHub", Description: "不经过第三方代理"},
	},
}

// GitHubProxyOptions returns a copy so callers cannot mutate the catalog.
func GitHubProxyOptions() []ProxyOption {
	options := make([]ProxyOption, 0, len(githubProxyCatalog)+1)
	options = append(options, ProxyOption{
		ID:          ProxyAuto,
		Name:        "自动（推荐）",
		Description: "先使用检查成功的入口，下载或校验持续失败/低速时再切换",
	})
	for _, proxy := range githubProxyCatalog {
		options = append(options, proxy.ProxyOption)
	}
	options = append(options, ProxyOption{
		ID:          ProxyCustom,
		Name:        customProxyName,
		Description: "填写自己的 HTTP(S) 加速地址或 socks5://代理",
	})
	return options
}

func proxyCandidates(proxyID string) ([]githubProxy, bool) {
	candidates, err := proxyCandidatesWithURL(proxyID, "")
	return candidates, err == nil
}

func proxyCandidatesWithURL(proxyID, customURL string) ([]githubProxy, error) {
	proxyID = normalizeProxyID(proxyID)
	if proxyID == ProxyCustom {
		prefix, socks5Addr, err := parseCustomProxyURL(customURL)
		if err != nil {
			return nil, newUpdateError(ErrInvalidGitHubProxy, "自定义 GitHub 加速地址无效", err)
		}
		return []githubProxy{{
			ProxyOption: ProxyOption{
				ID:          ProxyCustom,
				Name:        customProxyName,
				Description: prefix,
			},
			prefix:     prefix,
			socks5Addr: socks5Addr,
		}}, nil
	}
	if proxyID == "" || proxyID == ProxyAuto {
		candidates := make([]githubProxy, len(githubProxyCatalog))
		copy(candidates, githubProxyCatalog)
		return candidates, nil
	}
	for _, proxy := range githubProxyCatalog {
		if proxy.ID == proxyID {
			return []githubProxy{proxy}, nil
		}
	}
	return nil, newUpdateError(ErrInvalidGitHubProxy, "未知的 GitHub 加速入口", nil)
}

func proxyByID(proxyID string) (githubProxy, bool) {
	candidates, ok := proxyCandidates(proxyID)
	if !ok || len(candidates) == 0 {
		return githubProxy{}, false
	}
	return candidates[0], true
}

// checksumProxyCandidates is evaluated only after the binary has been
// downloaded. Auto mode keeps the selected entry first, then allows the
// checksum request to use the remaining public entries. This preserves the
// binary's resumable transfer while recovering from a proxy that serves the
// binary but stalls or rejects the small checksum asset.
func checksumProxyCandidates(proxyID, customURL string, selected githubProxy) []githubProxy {
	return updateProxyCandidatesForMode(customURL, selected, normalizeProxyID(proxyID) == ProxyAuto)
}

func updateProxyCandidates(customURL string, selected githubProxy) []githubProxy {
	return updateProxyCandidatesForMode(customURL, selected, true)
}

func updateProxyCandidatesForMode(customURL string, selected githubProxy, allowFallback bool) []githubProxy {
	candidates := []githubProxy{selected}
	if !allowFallback {
		return candidates
	}

	autoCandidates, err := proxyCandidatesWithURL(ProxyAuto, customURL)
	if err != nil {
		return candidates
	}
	for _, candidate := range autoCandidates {
		if candidate.ID == selected.ID {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func normalizeCustomProxyURL(rawURL string) (string, error) {
	normalized, _, err := parseCustomProxyURL(rawURL)
	return normalized, err
}

func parseCustomProxyURL(rawURL string) (normalized, socks5Addr string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", errEmptyCustomProxyURL
	}
	if strings.ContainsAny(rawURL, "\r\n") {
		return "", "", errCustomProxyURLControlChar
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", "", errCustomProxyURLSyntax
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
		return "", "", errCustomProxyURLScheme
	}
	if parsed.Host == "" {
		return "", "", errCustomProxyURLHost
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errCustomProxyURLShape
	}
	if scheme == "socks5" || scheme == "socks5h" {
		if parsed.Path != "" && parsed.Path != "/" {
			return "", "", errCustomProxyURLShape
		}
		portText := parsed.Port()
		port, portErr := strconv.Atoi(portText)
		if parsed.Hostname() == "" || portErr != nil || port < 1 || port > 65535 {
			return "", "", errCustomProxyURLPort
		}
		return scheme + "://" + net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port)), net.JoinHostPort(parsed.Hostname(), strconv.Itoa(port)), nil
	}
	return strings.TrimRight(rawURL, "/") + "/", "", nil
}

func rewriteGitHubURL(proxy githubProxy, rawURL string) string {
	if proxy.prefix == "" || !isPublicGitHubURL(rawURL) {
		return rawURL
	}
	return proxy.prefix + rawURL
}

func isPublicGitHubURL(rawURL string) bool {
	url := strings.TrimSpace(rawURL)
	for _, host := range []string{
		"https://api.github.com",
		"https://github.com",
	} {
		if url == host || strings.HasPrefix(url, host+"/") {
			return true
		}
	}
	return false
}
