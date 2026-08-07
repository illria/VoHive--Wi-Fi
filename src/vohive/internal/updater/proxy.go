package updater

import "strings"

// ProxyOption is the safe, user-facing description of a GitHub endpoint.
// The actual URL prefix stays server-side so the frontend cannot make the
// updater fetch arbitrary URLs.
type ProxyOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type githubProxy struct {
	ProxyOption
	prefix string
}

const (
	ProxyAuto   = "auto"
	ProxyDirect = "direct"
)

func normalizeProxyID(proxyID string) string {
	proxyID = strings.ToLower(strings.TrimSpace(proxyID))
	if proxyID == "" {
		return ProxyAuto
	}
	return proxyID
}

// Public proxy services are intentionally fallback options, not mandatory
// dependencies. Their availability can change, so auto mode tries each one
// and ends with a direct GitHub request.
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
		Description: "按顺序尝试多个 GitHub 加速入口，失败后回退直连",
	})
	for _, proxy := range githubProxyCatalog {
		options = append(options, proxy.ProxyOption)
	}
	return options
}

func proxyCandidates(proxyID string) ([]githubProxy, bool) {
	proxyID = normalizeProxyID(proxyID)
	if proxyID == "" || proxyID == ProxyAuto {
		candidates := make([]githubProxy, len(githubProxyCatalog))
		copy(candidates, githubProxyCatalog)
		return candidates, true
	}
	for _, proxy := range githubProxyCatalog {
		if proxy.ID == proxyID {
			return []githubProxy{proxy}, true
		}
	}
	return nil, false
}

func proxyByID(proxyID string) (githubProxy, bool) {
	candidates, ok := proxyCandidates(proxyID)
	if !ok || len(candidates) == 0 {
		return githubProxy{}, false
	}
	return candidates[0], true
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
