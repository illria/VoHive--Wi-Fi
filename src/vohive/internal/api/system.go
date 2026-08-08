package api

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/updater"
	"github.com/iniwex5/vohive/pkg/logger"
)

func writeUpdateError(c *gin.Context, err error) {
	code := updater.ErrorCodeOf(err)
	status := http.StatusInternalServerError
	switch code {
	case string(updater.ErrUpdateInProgress), string(updater.ErrDockerUnsupported), string(updater.ErrNoUpdate):
		status = http.StatusConflict
	case string(updater.ErrInvalidCurrentVersion), string(updater.ErrUnsupportedArchitecture), string(updater.ErrInvalidGitHubProxy):
		status = http.StatusBadRequest
	case string(updater.ErrGitHubUnreachable), string(updater.ErrReleaseNotFound):
		status = http.StatusBadGateway
	}
	c.JSON(status, gin.H{
		"error":   code,
		"code":    code,
		"message": strings.TrimSpace(err.Error()),
	})
}

// handleCheckUpdate 检查系统更新
func (s *Server) handleCheckUpdate(c *gin.Context) {
	info, err := updater.CheckUpdateWithProxyURL(c.Query("proxy_id"), c.Query("proxy_url"))
	if err != nil {
		logger.Error("检查系统更新失败", "err", err)
		writeUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

// handleUpdateProxies returns the built-in GitHub endpoints without making a
// network request, so the user can choose an entry even when GitHub is blocked.
func (s *Server) handleUpdateProxies(c *gin.Context) {
	c.JSON(http.StatusOK, updater.GitHubProxyOptions())
}

// handleApplyUpdate 应用系统更新
func (s *Server) handleApplyUpdate(c *gin.Context) {
	var request struct {
		ProxyID             string `json:"proxy_id"`
		ProxyURL            string `json:"proxy_url"`
		AllowProxyFallback  bool   `json:"allow_proxy_fallback"`
	}
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_request",
			"code":    "invalid_request",
			"message": "更新请求格式无效",
		})
		return
	}
	status, err := updater.StartUpdateWithProxyURLAndFallback(request.ProxyID, request.ProxyURL, request.AllowProxyFallback)
	if err != nil {
		logger.Error("应用更新失败", "err", err)
		writeUpdateError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, status)
}

// handleUpdateStatus 返回更新任务的持久化状态，供前端轮询。
func (s *Server) handleUpdateStatus(c *gin.Context) {
	c.JSON(http.StatusOK, updater.CurrentStatus())
}
