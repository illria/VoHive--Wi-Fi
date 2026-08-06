package device

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

type mockStartupUIMResetter struct {
	called int
	err    error
}

func (m *mockStartupUIMResetter) UIMReset(ctx context.Context) error {
	m.called++
	return m.err
}

type mockStartupProvisioningEnsurer struct {
	called int
	err    error
}

type startupSIMProbeStub struct {
	status     qmi.SIMStatus
	statusErr  error
	statusErrs []error
	iccid      string
	iccidErrs  []error
	statusCall int
	iccidCall  int
}

func (s *startupSIMProbeStub) GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error) {
	s.statusCall++
	if len(s.statusErrs) > 0 {
		err := s.statusErrs[0]
		s.statusErrs = s.statusErrs[1:]
		return s.status, err
	}
	return s.status, s.statusErr
}

func (s *startupSIMProbeStub) GetICCID(ctx context.Context) (string, error) {
	s.iccidCall++
	if len(s.iccidErrs) > 0 {
		err := s.iccidErrs[0]
		s.iccidErrs = s.iccidErrs[1:]
		return "", err
	}
	return s.iccid, nil
}

func (m *mockStartupProvisioningEnsurer) EnsureSIMProvisioned(ctx context.Context, opts qmimanager.EnsureSIMProvisionedOptions) (qmimanager.UIMReadiness, error) {
	m.called++
	return qmimanager.UIMReadiness{}, m.err
}

func TestPerformStartupQMIUIMReset(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	ensurer := &mockStartupProvisioningEnsurer{}
	readyCheckCalled := 0
	readyCheck := func(ctx context.Context) (bool, error) {
		readyCheckCalled++
		return true, nil
	}

	res := performStartupQMIUIMReset("dev1", resetter, ensurer, readyCheck, time.Millisecond*50, time.Millisecond*10)
	if !res {
		t.Fatalf("expected true, got false")
	}

	if resetter.called != 1 {
		t.Errorf("expected resetter called 1 time, got %d", resetter.called)
	}

	if ensurer.called != 1 {
		t.Errorf("expected ensurer called 1 time, got %d", ensurer.called)
	}

	if readyCheckCalled != 1 {
		t.Errorf("expected readyCheck called 1 time, got %d", readyCheckCalled)
	}
}

func TestMaybePerformStartupQMIUIMResetSkipsWhenSIMReady(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{status: qmi.SIMReady}

	if !maybePerformStartupQMIUIMReset(context.Background(), "dev-ready", resetter, nil, source,
		func() bool { return true }, &attempted, 10*time.Millisecond, time.Millisecond) {
		t.Fatal("ready SIM should be accepted")
	}
	if resetter.called != 0 || attempted.Load() {
		t.Fatalf("ready SIM reset state = calls:%d attempted:%v, want no reset", resetter.called, attempted.Load())
	}
}

func TestMaybePerformStartupQMIUIMResetUsesReadableICCIDFallback(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{
		statusErrs: []error{errors.New("UIM service temporarily unavailable")},
		iccid:      "8986001234567890123",
	}

	if !maybePerformStartupQMIUIMReset(context.Background(), "dev-iccid", resetter, nil, source,
		func() bool { return true }, &attempted, 10*time.Millisecond, time.Millisecond) {
		t.Fatal("readable ICCID should be accepted when SIM status has a transient error")
	}
	if resetter.called != 0 {
		t.Fatalf("reset calls = %d, want 0", resetter.called)
	}
}

func TestMaybePerformStartupQMIUIMResetDoesNotResetInitializingSIM(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{status: qmi.SIMNotReady}

	if maybePerformStartupQMIUIMReset(context.Background(), "dev-initializing", resetter, nil, source,
		func() bool { return true }, &attempted, 5*time.Millisecond, time.Millisecond) {
		t.Fatal("initializing SIM should not be reported ready")
	}
	if resetter.called != 0 || attempted.Load() {
		t.Fatalf("initializing SIM reset state = calls:%d attempted:%v, want no reset", resetter.called, attempted.Load())
	}
}

func TestMaybePerformStartupQMIUIMResetSkipsTerminalSIMStates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    qmi.SIMStatus
		statusErr error
	}{
		{name: "absent", status: qmi.SIMAbsent},
		{name: "pin", status: qmi.SIMPINRequired},
		{name: "puk", status: qmi.SIMPUKRequired},
		{name: "blocked", status: qmi.SIMBlocked},
		{name: "network_locked", status: qmi.SIMNetworkLocked},
		{name: "no_atr", statusErr: errors.New("card has no ATR")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetter := &mockStartupUIMResetter{}
			var attempted atomic.Bool
			source := &startupSIMProbeStub{status: tc.status, statusErr: tc.statusErr}
			if maybePerformStartupQMIUIMReset(context.Background(), "dev-terminal", resetter, nil, source,
				func() bool { return true }, &attempted, 10*time.Millisecond, time.Millisecond) {
				t.Fatal("terminal SIM state should not be reported ready")
			}
			if resetter.called != 0 || attempted.Load() {
				t.Fatalf("terminal SIM reset state = calls:%d attempted:%v, want no reset", resetter.called, attempted.Load())
			}
		})
	}
}

func TestMaybePerformStartupQMIUIMResetSkipsTransportFailure(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{statusErrs: []error{errors.New("write failed: broken pipe")}}

	if maybePerformStartupQMIUIMReset(context.Background(), "dev-transport", resetter, nil, source,
		func() bool { return true }, &attempted, 10*time.Millisecond, time.Millisecond) {
		t.Fatal("transport failure should not be reported ready")
	}
	if resetter.called != 0 || attempted.Load() {
		t.Fatalf("transport failure reset state = calls:%d attempted:%v, want no reset", resetter.called, attempted.Load())
	}
}

func TestMaybePerformStartupQMIUIMResetAllowsOnlyOneServiceFaultReset(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{
		status:    qmi.SIMNotReady,
		statusErr: errors.New("UIM service not ready"),
	}

	maybePerformStartupQMIUIMReset(context.Background(), "dev-service", resetter, nil, source,
		func() bool { return true }, &attempted, 8*time.Millisecond, time.Millisecond)
	if resetter.called != 1 || !attempted.Load() {
		t.Fatalf("service fault reset state = calls:%d attempted:%v, want one reset", resetter.called, attempted.Load())
	}

	source.statusErr = errors.New("UIM service not ready")
	maybePerformStartupQMIUIMReset(context.Background(), "dev-service", resetter, nil, source,
		func() bool { return true }, &attempted, 8*time.Millisecond, time.Millisecond)
	if resetter.called != 1 {
		t.Fatalf("repeated service fault reset calls = %d, want one per Worker lifecycle", resetter.called)
	}
}

func TestMaybePerformStartupQMIUIMResetHonorsCancellation(t *testing.T) {
	resetter := &mockStartupUIMResetter{}
	var attempted atomic.Bool
	source := &startupSIMProbeStub{status: qmi.SIMNotReady}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if maybePerformStartupQMIUIMReset(ctx, "dev-canceled", resetter, nil, source,
		func() bool { return true }, &attempted, time.Second, time.Millisecond) {
		t.Fatal("canceled startup wait should not report ready")
	}
	if resetter.called != 0 || attempted.Load() {
		t.Fatalf("canceled startup reset state = calls:%d attempted:%v, want no reset", resetter.called, attempted.Load())
	}
}
