package updater

import (
	"errors"
	"net/url"
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
	prefix string
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
	errCustomProxyURLScheme      = errors.New("地址必须使用 http 或 https")
	errCustomProxyURLHost        = errors.New("地址必须包含主机名")
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
// then pins the first successful entry for the rest of the update task.
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
		Description: "检查阶段自动选择入口，下载和校验阶段保持不变",
	})
	for _, proxy := range githubProxyCatalog {
		options = append(options, proxy.ProxyOption)
	}
	options = append(options, ProxyOption{
		ID:          ProxyCustom,
		Name:        customProxyName,
		Description: "填写自己的 HTTP(S) GitHub 加速地址",
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
		prefix, err := normalizeCustomProxyURL(customURL)
		if err != nil {
			return nil, newUpdateError(ErrInvalidGitHubProxy, "自定义 GitHub 加速地址无效", err)
		}
		return []githubProxy{{
			ProxyOption: ProxyOption{
				ID:          ProxyCustom,
				Name:        customProxyName,
				Description: prefix,
			},
			prefix: prefix,
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

func normalizeCustomProxyURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", errEmptyCustomProxyURL
	}
	if strings.ContainsAny(rawURL, "\r\n") {
		return "", errCustomProxyURLControlChar
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", errCustomProxyURLSyntax
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errCustomProxyURLScheme
	}
	if parsed.Host == "" {
		return "", errCustomProxyURLHost
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errCustomProxyURLShape
	}
	return strings.TrimRight(rawURL, "/") + "/", nil
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
