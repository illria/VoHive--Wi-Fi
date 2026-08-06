package manager

import (
	"context"
	"errors"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

// ErrSIMPINNotRequired indicates that a VerifyPIN request was rejected because
// the current card status is not PIN required. No VerifyPIN request is sent.
var ErrSIMPINNotRequired = errors.New("sim pin not required")

// ErrSIMPINNoRetries indicates that QMI reports a PIN-required state but no
// verification attempts remain. The caller must not send VerifyPIN.
var ErrSIMPINNoRetries = errors.New("sim pin retries exhausted")

// SIMPINVerifyError is returned after VerifyPIN has been sent and failed. The
// error text deliberately does not include the PIN or the modem's raw error.
// Callers should use the returned card details/status for the latest retry
// counters and state.
type SIMPINVerifyError struct {
	Cause            error
	StatusReadFailed bool
}

func (e *SIMPINVerifyError) Error() string {
	return "SIM PIN verification failed"
}

func (e *SIMPINVerifyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// GetSIMSecurityState reads the UIM card details required by the web SIM
// security flow. It intentionally uses UIM GetCardStatusDetails instead of
// the reduced DMS SIM state so PIN/PUK counters and UPIN selection remain
// available to higher layers.
func (m *Manager) GetSIMSecurityState(ctx context.Context) (*qmi.CardStatusDetails, qmi.SIMStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, qmi.SIMNotReady, err
	}

	type result struct {
		details *qmi.CardStatusDetails
		status  qmi.SIMStatus
	}
	got, err := withUIMRecoveryValue(m, "GetSIMSecurityState.GetCardStatusDetails", func(uim *qmi.UIMService) (result, error) {
		if err := ctx.Err(); err != nil {
			return result{}, err
		}
		details, status, err := uim.GetCardStatusDetails(ctx)
		return result{details: details, status: status}, err
	})
	if err != nil {
		return nil, got.status, err
	}
	return got.details, got.status, nil
}

// VerifySIMPIN performs exactly one QMI UIM VerifyPIN after re-reading card
// status, then re-reads card status on both success and failure. It never
// retries VerifyPIN and does not invoke UIM reset, SIM power cycling, modem
// recovery, or worker recovery itself.
func (m *Manager) VerifySIMPIN(ctx context.Context, pin string) (*qmi.CardStatusDetails, qmi.SIMStatus, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, qmi.SIMNotReady, err
	}

	details, status, err := m.GetSIMSecurityState(ctx)
	if err != nil {
		return nil, status, err
	}
	if status != qmi.SIMPINRequired {
		return details, status, ErrSIMPINNotRequired
	}
	if details == nil {
		return nil, status, errors.New("SIM PIN status details unavailable")
	}

	pinID := qmi.UIMPinIDPIN1
	verifyRetries := details.PIN1Retries
	if details.UsesUPIN {
		pinID = qmi.UIMPinIDUPIN
		verifyRetries = details.UPINRetries
	}
	if verifyRetries == 0 {
		return details, status, ErrSIMPINNoRetries
	}

	if err := ctx.Err(); err != nil {
		return details, status, err
	}
	uim, err := m.ensureUIMService()
	if err != nil {
		return details, status, err
	}
	verifyErr := uim.VerifyPIN(ctx, pinID, pin)
	if verifyErr != nil {
		if ctx.Err() != nil {
			return details, status, &SIMPINVerifyError{Cause: verifyErr}
		}
		finalDetails, finalStatus, readErr := m.GetSIMSecurityState(ctx)
		if readErr == nil {
			return finalDetails, finalStatus, &SIMPINVerifyError{Cause: verifyErr}
		}
		return details, status, &SIMPINVerifyError{Cause: verifyErr, StatusReadFailed: true}
	}

	if err := ctx.Err(); err != nil {
		return details, status, err
	}
	finalDetails, finalStatus, readErr := m.GetSIMSecurityState(ctx)
	if readErr != nil {
		return details, status, readErr
	}
	return finalDetails, finalStatus, nil
}
