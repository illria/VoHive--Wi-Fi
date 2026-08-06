package device

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vohive/internal/backend"
	qmipkg "github.com/iniwex5/vohive/internal/qmi"
	"github.com/iniwex5/vohive/pkg/logger"
)

var startupSIMAuthLogicalChannelsToClose = []int{1, 2, 3, 4}

const (
	startupQMIReadyWaitTimeout       = 30 * time.Second
	startupQMIReadyPollInterval      = time.Second
	startupQMIServiceErrorThreshold  = 3
	startupSIMReadinessReady         = "ready"
	startupSIMReadinessInitializing  = "initializing"
	startupSIMReadinessServiceError  = "service_error"
	startupSIMReadinessTransportDown = "transport_down"
	startupSIMReadinessTerminal      = "terminal"
)

type startupUIMResetter interface {
	UIMReset(ctx context.Context) error
}

type startupSIMStatusSource interface {
	GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error)
}

type startupSIMIdentitySource interface {
	startupSIMStatusSource
	GetICCID(ctx context.Context) (string, error)
}

type startupProvisioningEnsurer interface {
	EnsureSIMProvisioned(ctx context.Context, opts qmimanager.EnsureSIMProvisionedOptions) (qmimanager.UIMReadiness, error)
}

// 编译期保证 *qmipkg.Manager 满足 ensurer 接口；签名漂移将直接 break build 而非静默跳过。
var _ startupProvisioningEnsurer = (*qmipkg.Manager)(nil)
var _ startupSIMIdentitySource = (*qmipkg.Manager)(nil)

type startupSIMReadiness struct {
	Ready  bool
	State  string
	Reason string
	Err    error
}

func cleanupWorkerStartupSIMAuthLogicalChannels(w *Worker) {
	if w == nil || w.Backend == nil {
		return
	}
	if w.QMICore != nil {
		if !w.startupSIMCleanupStarted.CompareAndSwap(false, true) {
			logger.Debug("启动期 SIM 清理已在当前 Worker 生命周期执行，跳过重复进入", "device", w.ID)
			return
		}
		w.cacheMu.RLock()
		cachedIdentityReady := w.state.Identity.Ready &&
			(strings.TrimSpace(w.state.Identity.ICCID) != "" || strings.TrimSpace(w.state.Identity.IMSI) != "")
		w.cacheMu.RUnlock()
		if cachedIdentityReady {
			logger.Info("SIM 身份缓存已就绪，跳过启动期 QMI UIM reset", "device", w.ID, "reason", "cached_identity")
			return
		}
		ctx, finish := startupWorkerContext(w)
		defer finish()
		controlReady := func() bool {
			controlPath := strings.TrimSpace(w.QMICore.ControlDevice())
			return controlPath != "" && qmiControlPathStatOK(controlPath)
		}
		maybePerformStartupQMIUIMReset(
			ctx,
			w.ID,
			w.QMICore,
			w.QMICore,
			w.QMICore,
			controlReady,
			&w.startupUIMResetAttempted,
			startupQMIReadyWaitTimeout,
			startupQMIReadyPollInterval,
		)
		return
	}
	auth, ok := w.Backend.(backend.SIMAuthProvider)
	if !ok {
		logger.Debug("启动期跳过 SIMAuth 逻辑通道清理：backend 不支持 SIMAuthProvider",
			"device", w.ID)
		return
	}
	ctx, finish := startupWorkerContext(w)
	defer finish()
	for _, channelID := range startupSIMAuthLogicalChannelsToClose {
		closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := auth.CloseLogicalChannel(closeCtx, channelID)
		cancel()
		if err != nil {
			logger.Debug("启动期 SIMAuth 逻辑通道清理失败",
				"device", w.ID,
				"backend", w.Backend.Mode(),
				"channel", channelID,
				"err", err)
		}
	}
}

func startupWorkerContext(w *Worker) (context.Context, func()) {
	parent := context.Background()
	if w != nil && w.Pool != nil && w.Pool.ctx != nil {
		parent = w.Pool.ctx
	}
	ctx, cancel := context.WithCancel(parent)
	if w == nil || w.stop == nil {
		return ctx, cancel
	}

	done := make(chan struct{})
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			close(done)
			cancel()
		})
	}
	go func() {
		select {
		case <-w.stop:
			cancel()
		case <-done:
		}
	}()
	return ctx, finish
}

func startupQMISIMReadyCheck(source startupSIMStatusSource) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		if source == nil {
			return false, nil
		}
		status, err := source.GetSIMStatus(ctx)
		if err != nil {
			return false, err
		}
		return status == qmi.SIMReady, nil
	}
}

func startupSIMTerminalStatus(status qmi.SIMStatus) bool {
	switch status {
	case qmi.SIMAbsent, qmi.SIMPINRequired, qmi.SIMPUKRequired, qmi.SIMBlocked, qmi.SIMNetworkLocked:
		return true
	default:
		return false
	}
}

func startupSIMTerminalError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"no atr",
		"atr not",
		"card absent",
		"sim absent",
		"pin required",
		"puk required",
		"sim blocked",
		"network locked",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func startupSIMReadinessProbe(ctx context.Context, source startupSIMStatusSource) startupSIMReadiness {
	if ctx == nil {
		ctx = context.Background()
	}
	if source == nil {
		return startupSIMReadiness{State: startupSIMReadinessServiceError, Reason: "source_unavailable"}
	}

	status, statusErr := source.GetSIMStatus(ctx)
	if statusErr == nil {
		if status == qmi.SIMReady {
			return startupSIMReadiness{Ready: true, State: startupSIMReadinessReady, Reason: "sim_ready"}
		}
		if startupSIMTerminalStatus(status) {
			return startupSIMReadiness{State: startupSIMReadinessTerminal, Reason: status.String()}
		}

		if identitySource, ok := source.(startupSIMIdentitySource); ok {
			iccid, err := identitySource.GetICCID(ctx)
			if err == nil && strings.TrimSpace(iccid) != "" {
				return startupSIMReadiness{Ready: true, State: startupSIMReadinessReady, Reason: "iccid_readable"}
			}
			if err != nil && qmiErrorIndicatesTransportDown(err.Error()) {
				return startupSIMReadiness{State: startupSIMReadinessTransportDown, Reason: "iccid_transport_down", Err: err}
			}
		}
		return startupSIMReadiness{State: startupSIMReadinessInitializing, Reason: status.String()}
	}

	if startupSIMTerminalError(statusErr) {
		return startupSIMReadiness{State: startupSIMReadinessTerminal, Reason: "terminal_error", Err: statusErr}
	}
	if identitySource, ok := source.(startupSIMIdentitySource); ok {
		iccid, err := identitySource.GetICCID(ctx)
		if err == nil && strings.TrimSpace(iccid) != "" {
			return startupSIMReadiness{Ready: true, State: startupSIMReadinessReady, Reason: "iccid_readable"}
		}
		if err != nil && qmiErrorIndicatesTransportDown(err.Error()) {
			return startupSIMReadiness{State: startupSIMReadinessTransportDown, Reason: "iccid_transport_down", Err: err}
		}
	}
	if qmiErrorIndicatesTransportDown(statusErr.Error()) {
		return startupSIMReadiness{State: startupSIMReadinessTransportDown, Reason: "sim_status_transport_down", Err: statusErr}
	}
	return startupSIMReadiness{State: startupSIMReadinessServiceError, Reason: "sim_status_service_error", Err: statusErr}
}

func waitForStartupSIMReady(ctx context.Context, source startupSIMStatusSource, waitTimeout, pollInterval time.Duration) (startupSIMReadiness, int) {
	if ctx == nil {
		ctx = context.Background()
	}
	if waitTimeout <= 0 {
		waitTimeout = startupQMIReadyWaitTimeout
	}
	if pollInterval <= 0 {
		pollInterval = startupQMIReadyPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	last := startupSIMReadiness{State: startupSIMReadinessInitializing, Reason: "not_checked"}
	serviceErrors := 0
	for {
		if err := waitCtx.Err(); err != nil {
			last.Err = err
			return last, serviceErrors
		}
		probeCtx, probeCancel := context.WithTimeout(waitCtx, pollInterval)
		readiness := startupSIMReadinessProbe(probeCtx, source)
		probeCancel()
		last = readiness
		if readiness.Ready || readiness.State == startupSIMReadinessTerminal || readiness.State == startupSIMReadinessTransportDown {
			return readiness, serviceErrors
		}
		if readiness.State == startupSIMReadinessServiceError {
			serviceErrors++
		} else {
			serviceErrors = 0
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			last.Err = waitCtx.Err()
			return last, serviceErrors
		case <-timer.C:
		}
	}
}

func maybePerformStartupQMIUIMReset(ctx context.Context, deviceID string, resetter startupUIMResetter, ensurer startupProvisioningEnsurer, source startupSIMStatusSource, controlReady func() bool, resetAttempted *atomic.Bool, waitTimeout, pollInterval time.Duration) bool {
	readiness, serviceErrors := waitForStartupSIMReady(ctx, source, waitTimeout, pollInterval)
	if readiness.Ready {
		logger.Info("SIM 已就绪，跳过启动期 QMI UIM reset", "device", deviceID, "reason", readiness.Reason)
		return true
	}
	if readiness.State == startupSIMReadinessTerminal || readiness.State == startupSIMReadinessTransportDown {
		logger.Info("启动期跳过 QMI UIM reset：SIM 尚未满足 reset 条件", "device", deviceID, "state", readiness.State, "reason", readiness.Reason, "err", readiness.Err)
		return false
	}
	if serviceErrors < startupQMIServiceErrorThreshold {
		logger.Info("启动期 SIM 身份仍在收敛，暂不执行 QMI UIM reset", "device", deviceID, "state", readiness.State, "service_errors", serviceErrors, "last_err", readiness.Err)
		return false
	}
	if controlReady == nil || !controlReady() {
		logger.Info("启动期跳过 QMI UIM reset：QMI 控制节点未确认稳定", "device", deviceID, "state", readiness.State, "last_err", readiness.Err)
		return false
	}
	if resetAttempted != nil && !resetAttempted.CompareAndSwap(false, true) {
		logger.Debug("启动期 QMI UIM reset 已执行过，跳过重复 reset", "device", deviceID)
		return false
	}

	logger.Warn("启动期连续服务级异常，执行一次受保护的 QMI UIM reset", "device", deviceID, "service_errors", serviceErrors, "last_err", readiness.Err)
	return performStartupQMIUIMResetContext(ctx, deviceID, resetter, ensurer, func(checkCtx context.Context) (bool, error) {
		readiness := startupSIMReadinessProbe(checkCtx, source)
		return readiness.Ready, readiness.Err
	}, waitTimeout, pollInterval)
}

func performStartupQMIUIMReset(deviceID string, resetter startupUIMResetter, ensurer startupProvisioningEnsurer, readyCheck func(context.Context) (bool, error), waitTimeout, pollInterval time.Duration) bool {
	return performStartupQMIUIMResetContext(context.Background(), deviceID, resetter, ensurer, readyCheck, waitTimeout, pollInterval)
}

func performStartupQMIUIMResetContext(parent context.Context, deviceID string, resetter startupUIMResetter, ensurer startupProvisioningEnsurer, readyCheck func(context.Context) (bool, error), waitTimeout, pollInterval time.Duration) bool {
	if parent == nil {
		parent = context.Background()
	}
	if resetter == nil {
		return false
	}
	resetCtx, cancel := context.WithTimeout(parent, 3*time.Second)
	err := resetter.UIMReset(resetCtx)
	cancel()
	if err != nil {
		logger.Debug("启动期 QMI UIM reset 失败",
			"device", deviceID,
			"err", err)
		return false
	}
	logger.Debug("启动期 QMI UIM reset 已完成",
		"device", deviceID)
	if ensurer != nil {
		ensureCtx, cancel := context.WithTimeout(parent, 5*time.Second)
		if _, err := ensurer.EnsureSIMProvisioned(ensureCtx, qmimanager.EnsureSIMProvisionedOptions{}); err != nil {
			logger.Debug("启动期 QMI provisioning 收敛 best-effort 失败", "device", deviceID, "err", err)
		}
		cancel()
	}
	if readyCheck == nil {
		return true
	}
	if waitTimeout <= 0 {
		waitTimeout = 5 * time.Second
	}
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(waitTimeout)
	for {
		checkCtx, cancel := context.WithTimeout(parent, pollInterval)
		ready, err := readyCheck(checkCtx)
		cancel()
		if err == nil && ready {
			logger.Debug("启动期 QMI UIM reset 后 SIM ready",
				"device", deviceID)
			return true
		}
		if time.Now().After(deadline) {
			logger.Warn("启动期 QMI UIM reset 后等待 SIM ready 超时",
				"device", deviceID,
				"timeout", waitTimeout.String(),
				"last_err", err)
			return false
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-parent.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return false
		case <-timer.C:
		}
	}
}
