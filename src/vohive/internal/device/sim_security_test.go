package device

import (
	"context"
	"errors"
	"testing"

	"github.com/iniwex5/vohive/internal/backend"
)

func TestWorkerVerifySIMPINRejectsInvalidFormatBeforeProviderLookup(t *testing.T) {
	for _, pin := range []string{"", "123", "123456789", "12a4", " 1234", "１２３４"} {
		w := &Worker{}
		_, err := w.VerifySIMPIN(context.Background(), pin)
		var securityErr *SIMSecurityError
		if !errors.As(err, &securityErr) || securityErr.Code != simSecurityErrorInvalidPIN {
			t.Fatalf("PIN %q error = %v, want invalid_sim_pin_format", pin, err)
		}
	}
}

func TestWorkerSIMSecurityRejectsNonQMIBackend(t *testing.T) {
	w := &Worker{Backend: &workerStatusBackendStub{mode: backend.BackendAT}}
	_, err := w.VerifySIMPIN(context.Background(), "1234")
	var securityErr *SIMSecurityError
	if !errors.As(err, &securityErr) || securityErr.Code != simSecurityErrorUnsupported {
		t.Fatalf("error = %v, want unsupported_backend", err)
	}
}

func TestWorkerSIMSecurityPerDeviceOperationLockRejectsConcurrentRequest(t *testing.T) {
	w := &Worker{}
	w.simAuthMu.Lock()
	defer w.simAuthMu.Unlock()

	_, err := w.VerifySIMPIN(context.Background(), "1234")
	var securityErr *SIMSecurityError
	if !errors.As(err, &securityErr) || securityErr.Code != simSecurityErrorOperationBusy {
		t.Fatalf("error = %v, want sim_pin_operation_in_progress", err)
	}
}
