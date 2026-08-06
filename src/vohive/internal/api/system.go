package api

import (
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
	case string(updater.ErrInvalidCurrentVersion), string(updater.ErrUnsupportedArchitecture):
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
	info, err := updater.CheckUpdate()
	if err != nil {
		logger.Error("检查系统更新失败", "err", err)
		writeUpdateError(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

// handleApplyUpdate 应用系统更新
func (s *Server) handleApplyUpdate(c *gin.Context) {
	status, err := updater.StartUpdate()
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
