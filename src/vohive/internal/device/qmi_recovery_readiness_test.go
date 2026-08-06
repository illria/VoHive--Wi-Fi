package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/iniwex5/vohive/internal/config"
)

func TestQMIConvergenceShouldEscalate(t *testing.T) {
	if qmiConvergenceShouldEscalate(2, 3) {
		t.Fatal("streak below limit must not escalate")
	}
	if !qmiConvergenceShouldEscalate(3, 3) {
		t.Fatal("streak reaching limit must escalate")
	}
}

func TestConvergeQMIIdentityEscalatesOnPersistentTransportDown(t *testing.T) {
	origRefresh := convergeIdentityRefreshFn
	origEscalate := convergeEscalateFn
	origInterval := qmiConvergenceRetryInterval
	defer func() {
		convergeIdentityRefreshFn = origRefresh
		convergeEscalateFn = origEscalate
		qmiConvergenceRetryInterval = origInterval
	}()
	qmiConvergenceRetryInterval = time.Millisecond

	convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
		return errors.New("refresh_identity: write failed: write unix @->@qmi-proxy: write: broken pipe")
	}
	var mu sync.Mutex
	var escalations []string
	convergeEscalateFn = func(p *Pool, w *Worker, reason string, err error) {
		mu.Lock()
		escalations = append(escalations, reason)
		mu.Unlock()
	}

	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{ID: "dev-1", stop: make(chan struct{})}

	err := p.convergeQMIIdentity(context.Background(), w, "manual_reboot")
	if err == nil {
		t.Fatal("expected convergence to abort with error after persistent transport-down")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(escalations) != 1 || escalations[0] != "convergence_transport_down" {
		t.Fatalf("expected one convergence_transport_down escalation, got %v", escalations)
	}
}

func TestConvergeQMIIdentityEscalatesOnTimeout(t *testing.T) {
	origRefresh := convergeIdentityRefreshFn
	origEscalate := convergeEscalateFn
	origInterval := qmiConvergenceRetryInterval
	defer func() {
		convergeIdentityRefreshFn = origRefresh
		convergeEscalateFn = origEscalate
		qmiConvergenceRetryInterval = origInterval
	}()
	qmiConvergenceRetryInterval = time.Millisecond

	convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
		return errors.New("refresh_identity: live_identity_empty")
	}
	var mu sync.Mutex
	var escalations []string
	convergeEscalateFn = func(p *Pool, w *Worker, reason string, err error) {
		mu.Lock()
		escalations = append(escalations, reason)
		mu.Unlock()
	}

	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{ID: "dev-1", stop: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_ = p.convergeQMIIdentity(ctx, w, "manual_reboot")

	mu.Lock()
	defer mu.Unlock()
	if len(escalations) != 0 {
		t.Fatalf("identity-only timeout must not escalate to Worker rebuild, got %v", escalations)
	}
}

func TestQMIIdentityRetryDelayUsesTenThirtySixtyBackoff(t *testing.T) {
	orig := qmiIdentityRetryDelays
	defer func() { qmiIdentityRetryDelays = orig }()
	qmiIdentityRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, 60 * time.Second}

	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 10 * time.Second},
		{attempt: 1, want: 30 * time.Second},
		{attempt: 2, want: 60 * time.Second},
		{attempt: 3, want: 60 * time.Second},
	} {
		if got := qmiIdentityRetryDelay(tc.attempt); got != tc.want {
			t.Fatalf("qmiIdentityRetryDelay(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

func TestQMIIdentityConvergenceAllowsOnlyOneBackgroundRetryTask(t *testing.T) {
	origDelays := qmiIdentityRetryDelays
	defer func() { qmiIdentityRetryDelays = origDelays }()
	qmiIdentityRetryDelays = []time.Duration{time.Hour}

	p := NewPool(&config.Config{})
	w := &Worker{ID: "dev-1", Pool: p, stop: make(chan struct{})}
	p.mu.Lock()
	p.workers[w.ID] = w
	p.mu.Unlock()

	p.startQMIIdentityConvergence(w, "test")
	p.startQMIIdentityConvergence(w, "duplicate")
	w.identityRetryMu.Lock()
	running := w.identityRetryRunning
	w.identityRetryMu.Unlock()
	if !running {
		t.Fatal("expected one background identity retry task to be running")
	}

	close(w.stop)
	p.cancel()
}

func TestNonEssentialQMIWorkGatePausesIdentityPendingAndRecovery(t *testing.T) {
	w := &Worker{Config: config.DeviceConfig{DeviceBackend: "qmi"}}
	if paused, reason := w.qmiNonEssentialWorkPaused(); !paused || reason != "identity_pending" {
		t.Fatalf("initial QMI gate = paused:%v reason:%q, want identity_pending pause", paused, reason)
	}

	w.cacheMu.Lock()
	w.state.Identity.Ready = true
	w.state.Identity.Phase = simIdentityPhaseReady
	w.cacheMu.Unlock()
	if paused, reason := w.qmiNonEssentialWorkPaused(); paused || reason != "" {
		t.Fatalf("ready QMI gate = paused:%v reason:%q, want open", paused, reason)
	}

	w.RecordWatchdogEvent(WatchdogEvent{Layer: HealthLayerQMI, State: HealthStateRecovering, Reason: "transport_recovery"})
	if paused, reason := w.qmiNonEssentialWorkPaused(); !paused || reason != "qmi_recovery" {
		t.Fatalf("recovering QMI gate = paused:%v reason:%q, want qmi_recovery pause", paused, reason)
	}
}
