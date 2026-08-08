package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/pkg/logger"
)

type wecomTextPayload struct {
	MsgType string         `json:"msgtype"`
	Text    wecomTextBody `json:"text"`
}

type wecomTextBody struct {
	Content string `json:"content"`
}

type wecomResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// WecomChannel implements a one-way Enterprise WeChat group robot webhook.
type WecomChannel struct {
	webhookURL string
	client     *http.Client
}

// NewWecomChannel creates an Enterprise WeChat robot channel from configuration.
func NewWecomChannel(cfg config.WecomConfig) (*WecomChannel, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	webhookURL := strings.TrimSpace(cfg.WebhookURL)
	if webhookURL == "" {
		return nil, errors.New("企业微信机器人 webhook URL 不能为空")
	}
	parsed, err := url.Parse(webhookURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("企业微信机器人 webhook URL 必须是有效的 HTTP(S) 地址")
	}

	return &WecomChannel{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (c *WecomChannel) Name() string { return "wecom" }

func (c *WecomChannel) Send(text string) error {
	return c.SendWithContext(NotificationContext{Event: "通知", Text: text})
}

func (c *WecomChannel) SendWithContext(ctx NotificationContext) error {
	if c == nil || c.client == nil {
		return nil
	}

	text := strings.TrimSpace(ctx.Text)
	if text == "" {
		return nil
	}

	body, err := json.Marshal(wecomTextPayload{
		MsgType: "text",
		Text:    wecomTextBody{Content: text},
	})
	if err != nil {
		return fmt.Errorf("序列化企业微信机器人消息失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建企业微信机器人请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "VoHive-WeCom/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		logger.Warn("企业微信机器人发送失败", "err", err)
		return fmt.Errorf("企业微信机器人请求失败: %w", err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if readErr != nil {
		return fmt.Errorf("读取企业微信机器人响应失败: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("企业微信机器人返回 HTTP %d", resp.StatusCode)
	}

	var result wecomResponse
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("解析企业微信机器人响应失败: %w", err)
	}
	if result.ErrCode != 0 {
		if strings.TrimSpace(result.ErrMsg) == "" {
			return fmt.Errorf("企业微信机器人返回错误码 %d", result.ErrCode)
		}
		return fmt.Errorf("企业微信机器人返回错误 %d: %s", result.ErrCode, result.ErrMsg)
	}

	return nil
}

func (c *WecomChannel) RegisterCommand(_ string, _ CommandHandler) {}

func (c *WecomChannel) Start() error { return nil }

func (c *WecomChannel) Close() error {
	if c != nil && c.client != nil {
		c.client.CloseIdleConnections()
	}
	return nil
}
