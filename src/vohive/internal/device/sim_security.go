package device

import (
	"context"
	"errors"
	"time"

	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/pkg/logger"
)

const (
	simSecurityErrorInvalidPIN       = "invalid_sim_pin_format"
	simSecurityErrorDeviceRecovering = "device_recovering"
	simSecurityErrorOperationBusy    = "sim_pin_operation_in_progress"
	simSecurityErrorUnsupported      = "unsupported_backend"
	simSecurityErrorQMIUnavailable   = "qmi_uim_unavailable"
	simSecurityErrorUnavailable      = "sim_security_unavailable"
	simSecurityErrorNotRequired      = "sim_pin_not_required"
	simSecurityErrorPUKRequired      = "sim_puk_required"
	simSecurityErrorBlocked          = "sim_blocked"
	simSecurityErrorAbsent           = "sim_absent"
	simSecurityErrorNetworkLocked    = "sim_network_locked"
	simSecurityErrorIncorrect        = "sim_pin_incorrect"
	simSecurityErrorVerifyFailed     = "sim_pin_verify_failed"
)

// SIMSecurityError is a safe device-layer error. Its string is an API-safe
// code and never contains the submitted PIN or a raw modem error.
type SIMSecurityError struct {
	Code  string
	State backend.SIMSecurityState
}

func (e *SIMSecurityError) Error() string {
	if e == nil || e.Code == "" {
		return simSecurityErrorUnavailable
	}
	return e.Code
}

func newSIMSecurityError(code string, state backend.SIMSecurityState) error {
	return &SIMSecurityError{Code: code, State: state}
}

func validSIMPIN(pin string) bool {
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

func (w *Worker) simSecurityProvider() (backend.SIMSecurityProvider, error) {
	if w == nil || w.Backend == nil || w.Backend.Mode() != backend.BackendQMI {
		return nil, newSIMSecurityError(simSecurityErrorUnsupported, backend.SIMSecurityState{Status: backend.SIMSecurityUnsupported})
	}
	if w.stop != nil {
		select {
		case <-w.stop:
			return nil, newSIMSecurityError(simSecurityErrorDeviceRecovering, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
		default:
		}
	}
	if w.Pool != nil {
		if w.Pool.isWorkerRebuilding(w.ID) {
			return nil, newSIMSecurityError(simSecurityErrorDeviceRecovering, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
		}
		if w.Pool.IsESIMSwitching(w.ID) {
			return nil, newSIMSecurityError(simSecurityErrorOperationBusy, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
		}
		snapshot := w.Pool.LifecycleSnapshot(w.ID)
		if snapshot.Recovering || snapshot.Phase == LifecyclePhaseEvicting || lifecyclePhaseRecovering(snapshot.Phase) {
			return nil, newSIMSecurityError(simSecurityErrorDeviceRecovering, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
		}
	}
	if w.QMICore == nil || !w.QMICore.IsControlReady() {
		return nil, newSIMSecurityError(simSecurityErrorQMIUnavailable, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	provider, ok := w.Backend.(backend.SIMSecurityProvider)
	if !ok {
		return nil, newSIMSecurityError(simSecurityErrorQMIUnavailable, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	return provider, nil
}

func normalizeSIMSecurityState(state backend.SIMSecurityState) backend.SIMSecurityState {
	if state.Status == "" {
		state.Status = backend.SIMSecurityUnavailable
	}
	if state.Backend == "" {
		state.Backend = backend.BackendQMI
	}
	return state
}

func simSecurityStatusError(state backend.SIMSecurityState) string {
	switch state.Status {
	case backend.SIMSecurityPUKRequired:
		return simSecurityErrorPUKRequired
	case backend.SIMSecurityBlocked:
		return simSecurityErrorBlocked
	case backend.SIMSecurityAbsent:
		return simSecurityErrorAbsent
	case backend.SIMSecurityNetworkLocked:
		return simSecurityErrorNetworkLocked
	case backend.SIMSecurityPINRequired:
		return simSecurityErrorNotRequired
	default:
		return simSecurityErrorUnavailable
	}
}

func simSecurityProviderError(err error, state backend.SIMSecurityState) string {
	if errors.Is(err, backend.ErrSIMPINNotRequired) {
		return simSecurityStatusError(state)
	}
	if errors.Is(err, backend.ErrSIMPINNoRetries) {
		return simSecurityErrorVerifyFailed
	}
	if errors.Is(err, backend.ErrSIMPINIncorrect) {
		if state.Status == backend.SIMSecurityPINRequired {
			return simSecurityErrorIncorrect
		}
		return simSecurityStatusError(state)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return simSecurityErrorVerifyFailed
	}
	if errors.Is(err, backend.ErrSIMSecurityUnsupported) {
		return simSecurityErrorUnsupported
	}
	if errors.Is(err, backend.ErrSIMSecurityUnavailable) {
		return simSecurityErrorUnavailable
	}
	return simSecurityErrorVerifyFailed
}

func (w *Worker) GetSIMSecurityState(ctx context.Context) (backend.SIMSecurityState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return backend.SIMSecurityState{}, newSIMSecurityError(simSecurityErrorQMIUnavailable, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	if !w.simAuthMu.TryLock() {
		return backend.SIMSecurityState{}, newSIMSecurityError(simSecurityErrorOperationBusy, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	defer w.simAuthMu.Unlock()

	provider, err := w.simSecurityProvider()
	if err != nil {
		return backend.SIMSecurityState{}, err
	}
	state, err := provider.GetSIMSecurityState(ctx)
	state = normalizeSIMSecurityState(state)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return state, newSIMSecurityError(simSecurityErrorUnavailable, state)
		}
		return state, newSIMSecurityError(simSecurityErrorUnavailable, state)
	}
	return state, nil
}

func (w *Worker) VerifySIMPIN(ctx context.Context, pin string) (backend.SIMSecurityState, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Validate before acquiring a QMI provider so malformed input cannot reach
	// any modem-facing code, even when the device is unavailable.
	if !validSIMPIN(pin) {
		return backend.SIMSecurityState{}, newSIMSecurityError(simSecurityErrorInvalidPIN, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	if w == nil {
		return backend.SIMSecurityState{}, newSIMSecurityError(simSecurityErrorQMIUnavailable, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	if !w.simAuthMu.TryLock() {
		return backend.SIMSecurityState{}, newSIMSecurityError(simSecurityErrorOperationBusy, backend.SIMSecurityState{Status: backend.SIMSecurityUnavailable, Backend: backend.BackendQMI})
	}
	provider, providerErr := w.simSecurityProvider()
	if providerErr != nil {
		w.simAuthMu.Unlock()
		return backend.SIMSecurityState{}, providerErr
	}
	state, err := provider.VerifySIMPIN(ctx, pin)
	state = normalizeSIMSecurityState(state)
	if err != nil {
		code := simSecurityProviderError(err, state)
		w.simAuthMu.Unlock()
		logger.Warn("SIM PIN 验证未完成", "event", "SIM_PIN_VERIFY_FAILED", "device", w.ID, "reason", code, "remaining_retries", state.PINRetries)
		return state, newSIMSecurityError(code, state)
	}
	if state.Status != backend.SIMSecurityReady {
		code := simSecurityStatusError(state)
		w.simAuthMu.Unlock()
		return state, newSIMSecurityError(code, state)
	}
	w.simAuthMu.Unlock()

	// Identity refresh is intentionally outside simAuthMu and does not trigger
	// convergence, restart, power-cycle, or worker rebuild.
	refreshCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, ok := w.Backend.(liveSIMIdentityReader); ok {
		_ = w.RefreshIdentityLive(refreshCtx, "sim_pin_verified")
	}
	logger.Info("SIM PIN 验证成功", "event", "SIM_PIN_VERIFY_SUCCESS", "device", w.ID, "pin_kind", state.PINKind)
	return state, nil
}
