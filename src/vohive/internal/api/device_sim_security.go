package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/internal/config"
	"github.com/iniwex5/vohive/internal/device"

	"github.com/gin-gonic/gin"
)

const simPINRequestBodyLimit = 1024

type verifySIMPINRequest struct {
	PIN string `json:"pin"`
}

func validSIMPinRequest(pin string) bool {
	if len(pin) < 4 || len(pin) > 8 {
		return false
	}
	for i := 0; i < len(pin); i++ {
		if pin[i] < '0' || pin[i] > '9' {
			return false
		}
	}
	return true
}

func decodeVerifySIMPINRequest(c *gin.Context) (verifySIMPINRequest, bool) {
	var req verifySIMPINRequest
	contentType := strings.TrimSpace(c.GetHeader("Content-Type"))
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return req, false
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, simPINRequestBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || !validSIMPinRequest(req.PIN) {
		return verifySIMPINRequest{}, false
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return verifySIMPINRequest{}, false
	}
	return req, true
}

func simSecurityStatusResponse(state backend.SIMSecurityState) backend.SIMSecurityState {
	if state.Status == "" {
		state.Status = backend.SIMSecurityUnavailable
	}
	return state
}

func simSecurityMessage(code string) string {
	switch code {
	case "invalid_sim_pin_format":
		return "SIM PIN 格式无效"
	case "device_not_found":
		return "设备未找到"
	case "device_not_running":
		return "设备当前未运行"
	case "sim_security_unavailable":
		return "SIM 安全状态暂不可用"
	case "sim_pin_not_required":
		return "当前 SIM 不需要验证 PIN"
	case "sim_puk_required":
		return "SIM 已要求输入 PUK，当前不支持 PUK 解锁"
	case "sim_blocked":
		return "SIM 已被永久阻塞"
	case "sim_absent":
		return "未检测到 SIM"
	case "sim_network_locked":
		return "SIM 处于网络锁定状态"
	case "sim_pin_incorrect":
		return "SIM PIN 不正确"
	case "sim_pin_verify_failed":
		return "SIM PIN 验证结果暂不可确认，请刷新状态"
	case "sim_pin_operation_in_progress":
		return "SIM PIN 操作正在进行"
	case "device_recovering":
		return "设备正在恢复中，请稍后再试"
	case "unsupported_backend":
		return "当前后端不支持 SIM PIN 操作"
	case "qmi_uim_unavailable":
		return "QMI UIM 当前不可用"
	default:
		return "SIM 安全操作暂不可用"
	}
}

func simSecurityHTTPStatus(code string) int {
	switch code {
	case "invalid_sim_pin_format":
		return http.StatusBadRequest
	case "device_not_found":
		return http.StatusNotFound
	case "device_not_running", "sim_pin_not_required", "sim_pin_operation_in_progress", "device_recovering":
		return http.StatusConflict
	case "sim_puk_required", "sim_blocked", "sim_absent", "sim_network_locked":
		return http.StatusLocked
	case "sim_pin_incorrect":
		return http.StatusUnprocessableEntity
	case "unsupported_backend":
		return http.StatusNotImplemented
	default:
		return http.StatusServiceUnavailable
	}
}

func writeSIMSecurityError(c *gin.Context, code string, state *backend.SIMSecurityState) {
	payload := gin.H{
		"ok":      false,
		"code":    code,
		"message": simSecurityMessage(code),
	}
	if state != nil && state.Status != "" {
		payload["security"] = simSecurityStatusResponse(*state)
	}
	c.JSON(simSecurityHTTPStatus(code), payload)
}

func writeSIMSecurityDeviceError(c *gin.Context, err error) {
	code := "sim_security_unavailable"
	var securityErr *device.SIMSecurityError
	if errors.As(err, &securityErr) && securityErr != nil && strings.TrimSpace(securityErr.Code) != "" {
		code = securityErr.Code
		state := securityErr.State
		writeSIMSecurityError(c, code, &state)
		return
	}
	writeSIMSecurityError(c, code, nil)
}

func (s *Server) simSecurityWorker(c *gin.Context) (*device.Worker, bool) {
	deviceID := deviceIDParam(c)
	if deviceID == "" {
		writeSIMSecurityError(c, "device_not_found", nil)
		return nil, false
	}
	if s != nil && s.pool != nil {
		if worker := s.pool.GetWorker(deviceID); worker != nil {
			return worker, true
		}
	}
	if cfg, err := config.GetDeviceByID(deviceID); err == nil && cfg != nil {
		writeSIMSecurityError(c, "device_not_running", nil)
		return nil, false
	}
	writeSIMSecurityError(c, "device_not_found", nil)
	return nil, false
}

func (s *Server) handleGetSIMSecurity(c *gin.Context) {
	worker, ok := s.simSecurityWorker(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	state, err := worker.GetSIMSecurityState(ctx)
	if err != nil {
		writeSIMSecurityDeviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, simSecurityStatusResponse(state))
}

func (s *Server) handleVerifySIMPIN(c *gin.Context) {
	req, ok := decodeVerifySIMPINRequest(c)
	if !ok {
		writeSIMSecurityError(c, "invalid_sim_pin_format", nil)
		return
	}
	worker, ok := s.simSecurityWorker(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	state, err := worker.VerifySIMPIN(ctx, req.PIN)
	if err != nil {
		writeSIMSecurityDeviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"message":  "SIM PIN 验证成功",
		"security": simSecurityStatusResponse(state),
	})
}
