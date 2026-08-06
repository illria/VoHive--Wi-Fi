package device

import (
	"context"
	"errors"
	"time"

	"github.com/iniwex5/vohive/pkg/logger"
)

var qmiIdentityConvergenceTimeout = 2 * time.Minute
var qmiConvergenceRetryInterval = 2 * time.Second

var qmiIdentityRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}

// qmiConvergenceTransportFailureLimit 是收敛期间允许的连续传输断开次数上限，
// 达到后判定 qmi-proxy 控制面已失联，升级为完整 Worker 重建。
var qmiConvergenceTransportFailureLimit = 3

var convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
	return p.refreshModemRebootRecoveredIdentity(w, reason)
}

var convergeEscalateFn func(*Pool, *Worker, string, error)

func defaultConvergeEscalate(p *Pool, w *Worker, reason string, err error) {
	p.handleTransportRecoveryExhausted(w, w.generation, HealthLayerQMI, reason, err)
}

func qmiConvergenceShouldEscalate(transportFailureStreak, limit int) bool {
	if limit <= 0 {
		limit = 1
	}
	return transportFailureStreak >= limit
}

func (p *Pool) convergeQMIIdentity(ctx context.Context, worker *Worker, reason string) error {
	if p == nil || worker == nil {
		return context.Canceled
	}
	if ctx == nil {
		ctx = p.ctx
	}
	ticker := time.NewTicker(qmiConvergenceRetryInterval)
	defer ticker.Stop()

	transportFailures := 0
	for {
		err := convergeIdentityRefreshFn(p, worker, reason)
		if err == nil {
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerQMI,
				State:     HealthStateHealthy,
				EventType: "qmi_identity_ready",
				Reason:    reason,
			})
			logger.Info("QMI 身份收敛完成", "device", worker.ID, "reason", reason)
			return nil
		}

		if qmiErrorIndicatesTransportDown(err.Error()) {
			transportFailures++
			if qmiConvergenceShouldEscalate(transportFailures, qmiConvergenceTransportFailureLimit) {
				logger.Warn("QMI 身份收敛检测到控制面持续断开，升级为 Worker 重建",
					"device", worker.ID,
					"reason", reason,
					"transport_failures", transportFailures,
					"err", err)
				escalateQMIConvergence(p, worker, "convergence_transport_down", err)
				return err
			}
		} else {
			transportFailures = 0
			if qmiErrorIndicatesIdentityPending(err.Error()) {
				worker.MarkSIMIdentityDegraded("identity_pending", err)
			}
		}

		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				logger.Warn("QMI 身份收敛超时，保留当前 Worker 并转入后台身份重试",
					"device", worker.ID,
					"reason", reason)
			}
			return ctx.Err()
		case <-worker.stop:
			return context.Canceled
		case <-ticker.C:
		}
	}
}

func qmiIdentityRetryDelay(attempt int) time.Duration {
	if len(qmiIdentityRetryDelays) == 0 {
		return time.Minute
	}
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(qmiIdentityRetryDelays) {
		attempt = len(qmiIdentityRetryDelays) - 1
	}
	return qmiIdentityRetryDelays[attempt]
}

func (w *Worker) beginQMIIdentityRetry() bool {
	if w == nil {
		return false
	}
	w.identityRetryMu.Lock()
	defer w.identityRetryMu.Unlock()
	if w.identityRetryRunning {
		return false
	}
	w.identityRetryRunning = true
	return true
}

func (w *Worker) finishQMIIdentityRetry() {
	if w == nil {
		return
	}
	w.identityRetryMu.Lock()
	w.identityRetryRunning = false
	w.identityRetryMu.Unlock()
}

func (p *Pool) runQMIIdentityRetry(worker *Worker, reason string) {
	if p == nil || worker == nil {
		return
	}
	defer worker.finishQMIIdentityRetry()
	parent := p.ctx
	if parent == nil {
		parent = context.Background()
	}

	transportFailures := 0
	for attempt := 0; ; attempt++ {
		delay := qmiIdentityRetryDelay(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-parent.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-worker.stop:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}

		if p.GetWorker(worker.ID) != worker {
			return
		}
		retryReason := "identity_retry:" + reason
		err := convergeIdentityRefreshFn(p, worker, retryReason)
		if err == nil {
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerQMI,
				State:     HealthStateHealthy,
				EventType: "qmi_identity_ready",
				Reason:    retryReason,
			})
			logger.Info("QMI 后台身份重试成功", "device", worker.ID, "attempt", attempt+1, "reason", reason)
			p.resolveAndApplyPolicy(worker, "identity_ready")
			return
		}

		if qmiErrorIndicatesTransportDown(err.Error()) {
			transportFailures++
			if qmiConvergenceShouldEscalate(transportFailures, qmiConvergenceTransportFailureLimit) {
				logger.Warn("QMI 后台身份重试检测到控制面持续断开，交回传输恢复路径",
					"device", worker.ID, "attempt", attempt+1, "reason", reason, "err", err)
				escalateQMIConvergence(p, worker, "convergence_transport_down", err)
				return
			}
			logger.Warn("QMI 后台身份重试检测到暂时性传输断开，等待传输恢复",
				"device", worker.ID, "attempt", attempt+1, "reason", reason, "err", err)
			continue
		}
		transportFailures = 0
		worker.MarkSIMIdentityDegraded("identity_pending", err)
		logger.Info("QMI 身份仍未收敛，保持 Worker 并按退避继续重试",
			"device", worker.ID,
			"attempt", attempt+1,
			"next_retry_in", qmiIdentityRetryDelay(attempt+1).String(),
			"reason", reason,
			"err", err)
	}
}

func (w *Worker) qmiNonEssentialWorkPaused() (bool, string) {
	if w == nil {
		return true, "worker_nil"
	}
	if w.QMICore == nil && !requiresQMICore(w.Config) {
		return false, ""
	}
	snapshot := w.HealthSnapshot()
	switch snapshot.State {
	case HealthStateRecovering, HealthStateReprobing, HealthStateSuspect, HealthStateInvalid, HealthStateFailed:
		return true, "qmi_recovery"
	}

	w.cacheMu.RLock()
	phase := w.state.Identity.Phase
	identityReady := w.state.Identity.Ready
	w.cacheMu.RUnlock()
	if phase == simIdentityPhaseTransitioning || phase == simIdentityPhaseDegraded || !identityReady {
		return true, "identity_pending"
	}
	return false, ""
}

// gateNonEssentialQMIWork 用于短信轮询、网络偏好和 VoWiFi 入口等非必要工作。
// 它只做门控，不触发恢复，避免业务流量反过来放大 QMI 控制面抖动。
func (w *Worker) gateNonEssentialQMIWork() bool {
	paused, reason := w.qmiNonEssentialWorkPaused()
	if w == nil {
		return paused
	}
	w.nonEssentialPollMu.Lock()
	changed := false
	if paused {
		if w.nonEssentialPollPauseReason != reason {
			w.nonEssentialPollPauseReason = reason
			changed = true
		}
	} else if w.nonEssentialPollPauseReason != "" {
		w.nonEssentialPollPauseReason = ""
		changed = true
	}
	w.nonEssentialPollMu.Unlock()

	if changed {
		if paused {
			logger.Info("QMI 恢复/身份收敛期间暂停非必要业务轮询", "device", w.ID, "reason", reason)
		} else {
			logger.Info("QMI 恢复完成，恢复非必要业务轮询", "device", w.ID)
		}
	}
	return paused
}

func escalateQMIConvergence(p *Pool, w *Worker, reason string, err error) {
	if convergeEscalateFn != nil {
		convergeEscalateFn(p, w, reason, err)
		return
	}
	defaultConvergeEscalate(p, w, reason, err)
}

func (p *Pool) startQMIIdentityConvergence(worker *Worker, reason string) {
	if p == nil || worker == nil {
		return
	}
	if !worker.beginQMIIdentityRetry() {
		logger.Debug("QMI 身份后台重试已在运行，跳过重复启动", "device", worker.ID, "reason", reason)
		return
	}
	worker.MarkSIMIdentityDegraded("identity_pending", errors.New("qmi_identity_pending"))
	worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:     HealthLayerQMI,
		State:     HealthStateHealthy,
		EventType: "qmi_identity_converging",
		Reason:    reason,
	})
	go p.runQMIIdentityRetry(worker, reason)
}
