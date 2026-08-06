package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/backend"
)

func decodeSIMSecurityTestRequest(t *testing.T, body, contentType string) (verifySIMPINRequest, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/devices/dev/sim/actions/verify-pin", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", contentType)
	return decodeVerifySIMPINRequest(ctx)
}

func TestValidSIMPinRequestRequiresASCII4To8DigitsWithoutTrim(t *testing.T) {
	valid := []string{"1234", "12345678"}
	for _, pin := range valid {
		if !validSIMPinRequest(pin) {
			t.Errorf("valid PIN %q rejected", pin)
		}
	}
	invalid := []string{"", "123", "123456789", "1234 ", " 1234", "１２３４", "12a4"}
	for _, pin := range invalid {
		if validSIMPinRequest(pin) {
			t.Errorf("invalid PIN %q accepted", pin)
		}
	}
}

func TestDecodeVerifySIMPINRequestRejectsUnknownAndMultipleJSON(t *testing.T) {
	if req, ok := decodeSIMSecurityTestRequest(t, `{"pin":"1234"}`, "application/json"); !ok || req.PIN != "1234" {
		t.Fatalf("valid JSON decoded as req=%+v ok=%v", req, ok)
	}
	for _, body := range []string{
		`{"pin":"1234","extra":true}`,
		`{"pin":"1234"}{"pin":"5678"}`,
		`{"pin":"1234"} trailing`,
	} {
		if _, ok := decodeSIMSecurityTestRequest(t, body, "application/json; charset=utf-8"); ok {
			t.Errorf("malformed/unknown body accepted: %s", body)
		}
	}
	if _, ok := decodeSIMSecurityTestRequest(t, `{"pin":"1234"}`, "text/plain"); ok {
		t.Fatal("non-JSON content type accepted")
	}
}

func TestWriteSIMSecurityErrorDoesNotEchoPIN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	state := backend.SIMSecurityState{
		Status:      backend.SIMSecurityPINRequired,
		PINRequired: true,
		PINRetries:  2,
		Backend:     backend.BackendQMI,
	}
	writeSIMSecurityError(ctx, "sim_pin_incorrect", &state)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "1234") || strings.Contains(recorder.Body.String(), "raw modem") {
		t.Fatalf("error response contains sensitive/raw content: %s", recorder.Body.String())
	}
}
