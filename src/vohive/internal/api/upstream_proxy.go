package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/db"
	"github.com/iniwex5/vohive/internal/upstreamproxy"
)

// ── 前置代理管理 API（主服务） ──

func normalizeUpstreamProxyPayload(existing *db.UpstreamProxy, req db.UpstreamProxy) db.UpstreamProxy {
	out := req
	out.ID = strings.TrimSpace(out.ID)
	out.Name = strings.TrimSpace(out.Name)
	out.Addr = strings.TrimSpace(out.Addr)
	out.Username = strings.TrimSpace(out.Username)
	out.Password = strings.TrimSpace(out.Password)

	if existing != nil {
		out.CreatedAt = existing.CreatedAt
		if out.Password == "" {
			out.Password = existing.Password
		}
	}
	return out
}

func probeUpstreamProxyConfig(c *gin.Context, proxy db.UpstreamProxy) (upstreamproxy.ProbeResult, error) {
	return upstreamproxy.ProbeSOCKS5(c.Request.Context(), upstreamproxy.ProbeConfig{
		ProxyAddr: proxy.Addr,
		Username:  proxy.Username,
		Password:  proxy.Password,
		Timeout:   5 * time.Second,
	})
}

// handleListUpstreamProxies 获取所有前置代理实例
func (s *Server) handleListUpstreamProxies(c *gin.Context) {
	proxies, err := db.ListUpstreamProxies()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	// 密码脱敏
	for i := range proxies {
		proxies[i].Password = maskSecret(proxies[i].Password)
	}
	c.JSON(http.StatusOK, proxies)
}

// handleCreateUpstreamProxy 创建前置代理实例
func (s *Server) handleCreateUpstreamProxy(c *gin.Context) {
	var req db.UpstreamProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	req = normalizeUpstreamProxyPayload(nil, req)
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "id 不能为空"})
		return
	}
	if req.Addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "addr 不能为空"})
		return
	}
	result, probeErr := probeUpstreamProxyConfig(c, req)
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}
	if err := db.UpsertUpstreamProxy(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理已保存，并已通过探测",
		"result":  result,
	})
}

// handleUpdateUpstreamProxy 更新前置代理实例
func (s *Server) handleUpdateUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	var req db.UpstreamProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	existing, err := db.GetUpstreamProxyByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在"})
		return
	}
	req.ID = id
	req = normalizeUpstreamProxyPayload(existing, req)
	if req.Addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "addr 不能为空"})
		return
	}
	result, probeErr := probeUpstreamProxyConfig(c, req)
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}
	if err := db.UpsertUpstreamProxy(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理已更新，并已通过探测",
		"result":  result,
	})
}

// handleDeleteUpstreamProxy 删除前置代理实例
func (s *Server) handleDeleteUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	if err := db.DeleteUpstreamProxy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "前置代理已删除"})
}

// handleProbeUpstreamProxy 探测前置代理是否支持标准 Socks5 + UDP Associate。
func (s *Server) handleProbeUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	proxy, err := db.GetUpstreamProxyByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if proxy == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在"})
		return
	}

	result, probeErr := upstreamproxy.ProbeSOCKS5(c.Request.Context(), upstreamproxy.ProbeConfig{
		ProxyAddr: proxy.Addr,
		Username:  proxy.Username,
		Password:  proxy.Password,
		Timeout:   5 * time.Second,
	})
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理探测成功",
		"result":  result,
	})
}

type upstreamProxyCountryRuleResponse struct {
	CountryCode     string    `json:"country_code"`
	CountryName     string    `json:"country_name"`
	MCCs            []string  `json:"mccs"`
	UpstreamProxyID string    `json:"upstream_proxy_id"`
	UpstreamProxyIDs []string `json:"upstream_proxy_ids"`
	PinnedProxyID   string    `json:"pinned_proxy_id,omitempty"`
	Enabled         bool      `json:"enabled"`
	Required        bool      `json:"required"`
	AutoFailover    bool      `json:"auto_failover"`
	UpdatedAt       time.Time `json:"updated_at"`
	Members         []upstreamProxyCountryMemberResponse `json:"members"`
}

type upstreamProxyCountryMemberResponse struct {
	UpstreamProxyID string    `json:"upstream_proxy_id"`
	Priority        int       `json:"priority"`
	Healthy         *bool     `json:"healthy,omitempty"`
	Stage           string    `json:"stage,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
	CheckedAt       time.Time `json:"checked_at,omitempty"`
}

func buildUpstreamProxyCountryRuleResponse(rule db.UpstreamProxyCountryRule) upstreamProxyCountryRuleResponse {
	display := upstreamproxy.CountryRuleDisplay(rule.CountryCode)
	members, _ := db.ListUpstreamProxyCountryMembers(rule.CountryCode)
	healthRows, _ := db.ListUpstreamProxyHealth()
	healthByID := make(map[string]db.UpstreamProxyHealth, len(healthRows))
	for _, health := range healthRows {
		healthByID[health.UpstreamProxyID] = health
	}
	ids := make([]string, 0, len(members))
	memberResponses := make([]upstreamProxyCountryMemberResponse, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.UpstreamProxyID)
		item := upstreamProxyCountryMemberResponse{UpstreamProxyID: member.UpstreamProxyID, Priority: member.Priority}
		if health, ok := healthByID[member.UpstreamProxyID]; ok && !health.CheckedAt.IsZero() {
			healthy := health.Healthy
			item.Healthy = &healthy
			item.Stage = health.Stage
			item.LastError = health.LastError
			item.CheckedAt = health.CheckedAt
		}
		memberResponses = append(memberResponses, item)
	}
	if len(ids) == 0 && strings.TrimSpace(rule.UpstreamProxyID) != "" {
		ids = append(ids, strings.TrimSpace(rule.UpstreamProxyID))
	}
	return upstreamProxyCountryRuleResponse{
		CountryCode:     display.CountryCode,
		CountryName:     display.CountryName,
		MCCs:            display.MCCs,
		UpstreamProxyID: strings.TrimSpace(rule.UpstreamProxyID),
		UpstreamProxyIDs: ids,
		PinnedProxyID:   strings.TrimSpace(rule.PinnedProxyID),
		Enabled:         rule.Enabled,
		Required:        rule.Required,
		AutoFailover:    rule.AutoFailover,
		UpdatedAt:       rule.UpdatedAt,
		Members:         memberResponses,
	}
}

func (s *Server) handleListUpstreamProxyCountries(c *gin.Context) {
	if !upstreamproxy.CountryTableReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcc_mnc_table_unavailable"})
		return
	}
	c.JSON(http.StatusOK, upstreamproxy.ListCountryDisplays())
}

func (s *Server) handleListUpstreamProxyCountryRules(c *gin.Context) {
	rules, err := db.ListUpstreamProxyCountryRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	out := make([]upstreamProxyCountryRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, buildUpstreamProxyCountryRuleResponse(rule))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleUpsertUpstreamProxyCountryRule(c *gin.Context) {
	if !upstreamproxy.CountryTableReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcc_mnc_table_unavailable"})
		return
	}
	countryCode := upstreamproxy.NormalizeCountryCode(countryCodeParam(c))
	if _, ok := upstreamproxy.MCCsForCountryCode(countryCode); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "国家代码不在 MCC/MNC 表中"})
		return
	}
	var req struct {
		UpstreamProxyID  string   `json:"upstream_proxy_id"`
		UpstreamProxyIDs []string `json:"upstream_proxy_ids"`
		PinnedProxyID    string   `json:"pinned_proxy_id"`
		Enabled          bool     `json:"enabled"`
		Required         bool     `json:"required"`
		AutoFailover     *bool    `json:"auto_failover"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	proxyIDs := append([]string(nil), req.UpstreamProxyIDs...)
	if len(proxyIDs) == 0 && strings.TrimSpace(req.UpstreamProxyID) != "" {
		proxyIDs = []string{strings.TrimSpace(req.UpstreamProxyID)}
	}
	if len(proxyIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "至少选择一个前置代理"})
		return
	}
	for _, proxyID := range proxyIDs {
		proxy, err := db.GetUpstreamProxyByID(proxyID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
			return
		}
		if proxy == nil {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在: " + proxyID})
			return
		}
	}
	autoFailover := true
	if req.AutoFailover != nil {
		autoFailover = *req.AutoFailover
	}
	rule := db.UpstreamProxyCountryRule{
		CountryCode:     countryCode,
		UpstreamProxyID: strings.TrimSpace(proxyIDs[0]),
		PinnedProxyID:   strings.TrimSpace(req.PinnedProxyID),
		Enabled:         req.Enabled,
		Required:        req.Required,
		AutoFailover:    autoFailover,
	}
	if err := db.UpsertUpstreamProxyCountryRuleSet(rule, proxyIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	rule.CountryCode = countryCode
	c.JSON(http.StatusOK, buildUpstreamProxyCountryRuleResponse(rule))
}

func (s *Server) handleDeleteUpstreamProxyCountryRule(c *gin.Context) {
	countryCode := upstreamproxy.NormalizeCountryCode(countryCodeParam(c))
	if err := db.DeleteUpstreamProxyCountryRule(countryCode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// maskSecret 将密码脱敏为 **** 格式
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}
