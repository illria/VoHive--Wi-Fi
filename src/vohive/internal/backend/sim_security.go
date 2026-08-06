package backend

import (
	"context"
	"errors"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

// SIMSecurityStatus is the stable status vocabulary exposed to the web API.
type SIMSecurityStatus string

const (
	SIMSecurityReady         SIMSecurityStatus = "ready"
	SIMSecurityPINRequired   SIMSecurityStatus = "pin_required"
	SIMSecurityPUKRequired   SIMSecurityStatus = "puk_required"
	SIMSecurityBlocked       SIMSecurityStatus = "blocked"
	SIMSecurityAbsent        SIMSecurityStatus = "absent"
	SIMSecurityNetworkLocked SIMSecurityStatus = "network_locked"
	SIMSecurityInitializing  SIMSecurityStatus = "initializing"
	SIMSecurityUnavailable   SIMSecurityStatus = "unavailable"
	SIMSecurityUnsupported   SIMSecurityStatus = "unsupported"
)

type SIMSecurityPINKind string

const (
	SIMSecurityPIN1 SIMSecurityPINKind = "pin1"
	SIMSecurityUPIN SIMSecurityPINKind = "upin"
)

// SIMSecurityState contains only the active PIN/UPIN state and retry counters.
// It deliberately has no PIN value or persistence-related fields.
type SIMSecurityState struct {
	Status       SIMSecurityStatus  `json:"status"`
	PINKind      SIMSecurityPINKind `json:"pin_kind,omitempty"`
	PINRequired  bool               `json:"pin_required"`
	PUKRequired  bool               `json:"puk_required"`
	Blocked      bool               `json:"blocked"`
	UsesUPIN     bool               `json:"uses_upin"`
	PINRetries   int                `json:"pin_retries,omitempty"`
	PUKRetries   int                `json:"puk_retries,omitempty"`
	CanVerifyPIN bool               `json:"can_verify_pin"`
	Backend      string             `json:"backend"`
	UpdatedAt    string             `json:"updated_at,omitempty"`
}

var (
	ErrSIMSecurityUnavailable = errors.New("sim security unavailable")
	ErrSIMSecurityUnsupported = errors.New("sim security unsupported")
	ErrSIMPINNotRequired      = errors.New("sim pin not required")
	ErrSIMPINNoRetries        = errors.New("sim pin retries exhausted")
	ErrSIMPINIncorrect        = errors.New("sim pin incorrect")
)

// SIMSecurityProvider is intentionally separate from DeviceBackend. Existing
// AT/MBIM test doubles and backends must not gain a PIN operation they cannot
// implement.
type SIMSecurityProvider interface {
	GetSIMSecurityState(ctx context.Context) (SIMSecurityState, error)
	VerifySIMPIN(ctx context.Context, pin string) (SIMSecurityState, error)
}

type qmiSIMSecuritySource interface {
	GetSIMSecurityState(ctx context.Context) (*qmi.CardStatusDetails, qmi.SIMStatus, error)
	VerifySIMPIN(ctx context.Context, pin string) (*qmi.CardStatusDetails, qmi.SIMStatus, error)
}

func mapQMIToSIMSecurityState(details *qmi.CardStatusDetails, status qmi.SIMStatus) SIMSecurityState {
	state := SIMSecurityState{
		Backend:   BackendQMI,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	switch status {
	case qmi.SIMReady:
		state.Status = SIMSecurityReady
	case qmi.SIMPINRequired:
		state.Status = SIMSecurityPINRequired
	case qmi.SIMPUKRequired:
		state.Status = SIMSecurityPUKRequired
	case qmi.SIMBlocked:
		state.Status = SIMSecurityBlocked
	case qmi.SIMAbsent:
		state.Status = SIMSecurityAbsent
	case qmi.SIMNetworkLocked:
		state.Status = SIMSecurityNetworkLocked
	case qmi.SIMNotReady:
		state.Status = SIMSecurityInitializing
	default:
		state.Status = SIMSecurityUnavailable
	}

	if details == nil {
		return state
	}

	state.UsesUPIN = details.UsesUPIN
	if details.UsesUPIN {
		state.PINKind = SIMSecurityUPIN
		state.PINRetries = int(details.UPINRetries)
		state.PUKRetries = int(details.UPUKRetries)
	} else {
		state.PINKind = SIMSecurityPIN1
		state.PINRetries = int(details.PIN1Retries)
		state.PUKRetries = int(details.PUK1Retries)
	}
	state.PINRequired = state.Status == SIMSecurityPINRequired
	state.PUKRequired = state.Status == SIMSecurityPUKRequired
	state.Blocked = state.Status == SIMSecurityBlocked
	state.CanVerifyPIN = state.PINRequired && state.PINRetries > 0
	return state
}

func (q *QMIBackend) simSecuritySource() (qmiSIMSecuritySource, error) {
	if q == nil || q.source == nil {
		return nil, ErrSIMSecurityUnavailable
	}
	source, ok := q.source.(qmiSIMSecuritySource)
	if !ok {
		return nil, ErrSIMSecurityUnavailable
	}
	return source, nil
}

// GetSIMSecurityState reads UIM card status details, rather than the reduced
// DMS SIM status, so the active PIN kind and retry counters are preserved.
func (q *QMIBackend) GetSIMSecurityState(ctx context.Context) (SIMSecurityState, error) {
	source, err := q.simSecuritySource()
	if err != nil {
		return SIMSecurityState{Status: SIMSecurityUnavailable, Backend: BackendQMI}, err
	}
	details, status, err := source.GetSIMSecurityState(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return mapQMIToSIMSecurityState(details, status), err
		}
		return mapQMIToSIMSecurityState(details, status), ErrSIMSecurityUnavailable
	}
	return mapQMIToSIMSecurityState(details, status), nil
}

// VerifySIMPIN translates raw QMI errors into safe business sentinels. The
// PIN never appears in an error, log, or returned state.
func (q *QMIBackend) VerifySIMPIN(ctx context.Context, pin string) (SIMSecurityState, error) {
	source, err := q.simSecuritySource()
	if err != nil {
		return SIMSecurityState{Status: SIMSecurityUnavailable, Backend: BackendQMI}, err
	}
	details, status, err := source.VerifySIMPIN(ctx, pin)
	state := mapQMIToSIMSecurityState(details, status)
	if err == nil {
		return state, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return state, err
	}
	if errors.Is(err, qmimanager.ErrSIMPINNotRequired) {
		return state, ErrSIMPINNotRequired
	}
	if errors.Is(err, qmimanager.ErrSIMPINNoRetries) {
		return state, ErrSIMPINNoRetries
	}
	var verifyErr *qmimanager.SIMPINVerifyError
	if errors.As(err, &verifyErr) {
		if verifyErr.StatusReadFailed {
			return state, ErrSIMSecurityUnavailable
		}
		return state, ErrSIMPINIncorrect
	}
	return state, ErrSIMSecurityUnavailable
}
