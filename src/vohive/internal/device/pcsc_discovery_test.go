package device

import (
	"context"
	"errors"
	"testing"

	"github.com/iniwex5/vohive/internal/backend"
	"github.com/iniwex5/vohive/internal/config"
)

func TestDiscoverPCSCReadersKeepsHealthyAndEmptySlots(t *testing.T) {
	originalList := listPCSCReadersFn
	originalProbe := probePCSCIdentityFn
	t.Cleanup(func() {
		listPCSCReadersFn = originalList
		probePCSCIdentityFn = originalProbe
	})
	listPCSCReadersFn = func(context.Context) ([]string, error) {
		return []string{"Reader A", "Reader B"}, nil
	}
	probePCSCIdentityFn = func(_ context.Context, reader string) (backend.PCSCIdentity, error) {
		if reader == "Reader B" {
			return backend.PCSCIdentity{}, errors.New("card absent")
		}
		return backend.PCSCIdentity{ReaderName: reader, ICCID: "8986001234567890123", IMSI: "460001234567890", MCC: "460", MNC: "00"}, nil
	}

	got, err := DiscoverPCSCReaders(context.Background())
	if err != nil {
		t.Fatalf("DiscoverPCSCReaders() error=%v", err)
	}
	if len(got) != 2 || got[0].ICCID != "8986001234567890123" || got[1].Error == "" {
		t.Fatalf("DiscoverPCSCReaders()=%+v", got)
	}
}

func TestNextPCSCDeviceIDUsesICCIDAndAvoidsCollision(t *testing.T) {
	devices := []config.DeviceConfig{{ID: "pcsc_67890123"}}
	if got := nextPCSCDeviceID("8986001234567890123", devices); got != "pcsc_67890123_2" {
		t.Fatalf("nextPCSCDeviceID()=%q", got)
	}
}
