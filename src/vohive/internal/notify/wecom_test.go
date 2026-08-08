package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iniwex5/vohive/internal/config"
)

func TestWecomSendWithContext(t *testing.T) {
	t.Parallel()

	type capturedPayload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	var got capturedPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type=%q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer server.Close()

	channel, err := NewWecomChannel(config.WecomConfig{Enabled: true, WebhookURL: server.URL})
	if err != nil {
		t.Fatalf("NewWecomChannel() error = %v", err)
	}
	defer channel.Close()

	if err := channel.SendWithContext(NotificationContext{
		Event:     "sms_received",
		Text:      "收到新短信\n内容 hello",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SendWithContext() error = %v", err)
	}
	if got.MsgType != "text" {
		t.Fatalf("msgtype=%q want text", got.MsgType)
	}
	if got.Text.Content != "收到新短信\n内容 hello" {
		t.Fatalf("content=%q", got.Text.Content)
	}
}

func TestWecomSendRejectsAPIError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":93000,"errmsg":"invalid webhook"}`))
	}))
	defer server.Close()

	channel, err := NewWecomChannel(config.WecomConfig{Enabled: true, WebhookURL: server.URL})
	if err != nil {
		t.Fatalf("NewWecomChannel() error = %v", err)
	}
	defer channel.Close()

	if err := channel.Send("test"); err == nil {
		t.Fatal("Send() should reject non-zero errcode")
	}
}

func TestNewWecomChannelValidatesURL(t *testing.T) {
	t.Parallel()

	for _, invalidURL := range []string{"", "qyapi.weixin.qq.com/cgi-bin/webhook/send", "ftp://example.com/webhook"} {
		if _, err := NewWecomChannel(config.WecomConfig{Enabled: true, WebhookURL: invalidURL}); err == nil {
			t.Fatalf("URL %q should be rejected", invalidURL)
		}
	}
}
