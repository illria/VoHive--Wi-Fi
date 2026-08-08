package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/internal/notify"
)

type testWecomRequest struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}

type testWecomResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (s *Server) handleTestWecomNotification(c *gin.Context) {
	var req testWecomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	if !req.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请先启用企业微信机器人后再测试"})
		return
	}

	webhookURL := strings.TrimSpace(req.WebhookURL)
	if webhookURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请填写企业微信机器人 webhook URL"})
		return
	}

	ch, err := notify.NewWecomChannel(config.WecomConfig{
		Enabled:    true,
		WebhookURL: webhookURL,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "企业微信机器人地址无效: " + err.Error()})
		return
	}
	if ch == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "企业微信机器人测试发送器未初始化"})
		return
	}
	defer ch.Close()

	err = ch.SendWithContext(notify.NotificationContext{
		Event:      "wecom_test",
		Text:       "这是一条来自 VoHive 的企业微信机器人测试通知",
		DeviceID:   "test_device_001",
		DeviceName: "测试设备",
		Timestamp:  time.Now(),
	})
	if err != nil {
		c.JSON(http.StatusOK, testWecomResponse{
			OK:      false,
			Message: "测试通知发送失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, testWecomResponse{
		OK:      true,
		Message: "企业微信机器人测试通知已发送",
	})
}
