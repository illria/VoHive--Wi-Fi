package backend

import (
	"context"
	"errors"
	"testing"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

func TestMapQMIToSIMSecurityStateUsesActivePINCounters(t *testing.T) {
	details := &qmi.CardStatusDetails{
		UsesUPIN:    true,
		PIN1Retries: 7,
		PUK1Retries: 8,
		UPINRetries: 2,
		UPUKRetries: 9,
	}
	got := mapQMIToSIMSecurityState(details, qmi.SIMPINRequired)
	if got.Status != SIMSecurityPINRequired || got.PINKind != SIMSecurityUPIN || !got.UsesUPIN {
		t.Fatalf("state identity = %+v, want UPIN/PIN required", got)
	}
	if got.PINRetries != 2 || got.PUKRetries != 9 || !got.CanVerifyPIN {
		t.Fatalf("state retries = %+v, want active UPIN counters", got)
	}

	details.UsesUPIN = false
	got = mapQMIToSIMSecurityState(details, qmi.SIMPINRequired)
	if got.PINKind != SIMSecurityPIN1 || got.PINRetries != 7 || got.PUKRetries != 8 {
		t.Fatalf("PIN1 state retries = %+v, want PIN1 counters", got)
	}
}

func TestMapQMIToSIMSecurityStateMapsStableStatuses(t *testing.T) {
	cases := []struct {
		status qmi.SIMStatus
		want   SIMSecurityStatus
	}{
		{qmi.SIMReady, SIMSecurityReady},
		{qmi.SIMPINRequired, SIMSecurityPINRequired},
		{qmi.SIMPUKRequired, SIMSecurityPUKRequired},
		{qmi.SIMBlocked, SIMSecurityBlocked},
		{qmi.SIMAbsent, SIMSecurityAbsent},
		{qmi.SIMNetworkLocked, SIMSecurityNetworkLocked},
		{qmi.SIMNotReady, SIMSecurityInitializing},
	}
	for _, tc := range cases {
		got := mapQMIToSIMSecurityState(&qmi.CardStatusDetails{}, tc.status)
		if got.Status != tc.want {
			t.Errorf("status %v mapped to %q, want %q", tc.status, got.Status, tc.want)
		}
	}
}

type simSecuritySourceStub struct {
	qmiBackendSendSourceStub
	details       *qmi.CardStatusDetails
	status        qmi.SIMStatus
	verifyErr     error
	verifyCalls   int
	lastVerifyPIN string
}

func (s *simSecuritySourceStub) GetSIMSecurityState(ctx context.Context) (*qmi.CardStatusDetails, qmi.SIMStatus, error) {
	return s.details, s.status, nil
}

func (s *simSecuritySourceStub) VerifySIMPIN(ctx context.Context, pin string) (*qmi.CardStatusDetails, qmi.SIMStatus, error) {
	s.verifyCalls++
	s.lastVerifyPIN = pin
	return s.details, s.status, s.verifyErr
}

func TestQMIBackendVerifySIMPINTranslatesSafeErrors(t *testing.T) {
	source := &simSecuritySourceStub{
		details: &qmi.CardStatusDetails{PIN1Retries: 3},
		status:  qmi.SIMPINRequired,
		verifyErr: &qmimanager.SIMPINVerifyError{
			Cause: errors.New("raw modem error must not escape backend"),
		},
	}
	be, err := NewQMIBackend("/dev/null", source)
	if err != nil {
		t.Fatalf("NewQMIBackend() error = %v", err)
	}
	state, err := be.VerifySIMPIN(context.Background(), "1234")
	if !errors.Is(err, ErrSIMPINIncorrect) {
		t.Fatalf("VerifySIMPIN() error = %v, want ErrSIMPINIncorrect", err)
	}
	if err.Error() != ErrSIMPINIncorrect.Error() || state.PINRetries != 3 {
		t.Fatalf("error/state leaked raw data: err=%q state=%+v", err, state)
	}
	if source.verifyCalls != 1 || source.lastVerifyPIN != "1234" {
		t.Fatalf("source calls = %d/%q, want one request with submitted PIN", source.verifyCalls, source.lastVerifyPIN)
	}
}
