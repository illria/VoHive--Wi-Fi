package device

import (
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/db"
	"github.com/iniwex5/vohive/pkg/logger"
	"github.com/iniwex5/vowifi-go/runtimehost"
)

type voWiFiSessionHistoryContext struct {
	TraceID         string
	UpstreamProxyID string
}

func (p *Pool) setVoWiFiSessionHistoryContext(deviceID, traceID, proxyID string) {
	if p == nil {
		return
	}
	p.vowifiSessionMu.Lock()
	if p.vowifiSessions == nil {
		p.vowifiSessions = make(map[string]voWiFiSessionHistoryContext)
	}
	p.vowifiSessions[strings.TrimSpace(deviceID)] = voWiFiSessionHistoryContext{
		TraceID:         strings.TrimSpace(traceID),
		UpstreamProxyID: strings.TrimSpace(proxyID),
	}
	p.vowifiSessionMu.Unlock()
}

func (p *Pool) voWiFiSessionHistoryContext(deviceID string) voWiFiSessionHistoryContext {
	p.vowifiSessionMu.RLock()
	ctx := p.vowifiSessions[strings.TrimSpace(deviceID)]
	p.vowifiSessionMu.RUnlock()
	return ctx
}

func voWiFiFailureStage(state runtimehost.State) string {
	if state.Phase != runtimehost.PhaseFailed {
		switch state.Phase {
		case runtimehost.PhaseStarting:
			return "Starting"
		case runtimehost.PhaseSIMReady:
			return "SIM"
		case runtimehost.PhaseTunnel:
			return "Tunnel"
		case runtimehost.PhaseIMSReady:
			return "IMS"
		case runtimehost.PhaseSMSReady:
			return "SMS"
		case runtimehost.PhaseStopped:
			return "Stopped"
		default:
			return "Unknown"
		}
	}
	if !state.SIMReady {
		return "SIM"
	}
	if !state.AccessReady {
		return "Access"
	}
	if !state.TunnelReady {
		return "Tunnel"
	}
	if !state.IMSReady {
		return "IMS"
	}
	if !state.SMSReady {
		return "SMS"
	}
	return "Runtime"
}

func (p *Pool) recordVoWiFiHistoryState(deviceID string, state runtimehost.State) {
	if p == nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		deviceID = strings.TrimSpace(state.DeviceID)
	}
	if deviceID == "" {
		return
	}
	iccid := ""
	if worker := p.GetWorker(deviceID); worker != nil {
		iccid = strings.TrimSpace(worker.CurrentICCID())
	}
	session := p.voWiFiSessionHistoryContext(deviceID)
	createdAt := state.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	event := db.VoWiFiConnectionEvent{
		DeviceID:        deviceID,
		ICCID:           iccid,
		Phase:           string(state.Phase),
		Stage:           voWiFiFailureStage(state),
		SIMReady:        state.SIMReady,
		AccessReady:     state.AccessReady,
		TunnelReady:     state.TunnelReady,
		IMSReady:        state.IMSReady,
		SMSReady:        state.SMSReady,
		ErrorClass:      strings.TrimSpace(state.LastErrorClass),
		Reason:          strings.TrimSpace(state.LastReason),
		Detail:          strings.TrimSpace(state.LastError),
		UpstreamProxyID: session.UpstreamProxyID,
		TraceID:         session.TraceID,
		CreatedAt:       createdAt,
	}
	if err := db.RecordVoWiFiConnectionEvent(event); err != nil {
		logger.Debug("记录 VoWiFi 连接历史失败", "device", deviceID, "err", err)
	}
}
