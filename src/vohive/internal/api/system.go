package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/updater"
	"github.com/iniwex5/vohive/pkg/logger"
)

// handleCheckUpdate 检查系统更新
func (s *Server) handleCheckUpdate(c *gin.Context) {
	info, err := updater.CheckUpdate()
	if err != nil {
		logger.Error("检查系统更新失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

// handleApplyUpdate 应用系统更新
func (s *Server) handleApplyUpdate(c *gin.Context) {
	go func() {
		if err := updater.ApplyUpdate(); err != nil {
			logger.Error("应用更新失败", "err", err)
		}
	}()
	c.JSON(http.StatusOK, gin.H{"message": "正在后台下载更新，系统稍后将自动重启..."})
}
