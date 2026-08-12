package device

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/damonto/uicc-go/ccid"
	"github.com/iniwex5/vohive/internal/backend"
)

const pcscProbeTimeout = 8 * time.Second

// PCSCReaderDevice is a physical reader/card snapshot. ReaderName identifies the
// slot while ICCID identifies the line and survives moving the card to a new slot.
type PCSCReaderDevice struct {
	ReaderName string
	ICCID      string
	IMSI       string
	MCC        string
	MNC        string
	Error      string
}

var listPCSCReadersFn = ccid.ListReaders
var probePCSCIdentityFn = backend.ProbePCSCIdentity

type pcscIdentityProbe func(context.Context, string) (backend.PCSCIdentity, error)

// DiscoverPCSCReaders lists every reader even when a slot is empty or a card
// cannot be decoded. A per-reader timeout prevents one faulty reader from
// blocking the complete device discovery response.
func DiscoverPCSCReaders(ctx context.Context) ([]PCSCReaderDevice, error) {
	return discoverPCSCReadersWithProbe(ctx, probePCSCIdentityFn)
}

func discoverPCSCReadersWithProbe(ctx context.Context, probe pcscIdentityProbe) ([]PCSCReaderDevice, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if probe == nil {
		probe = probePCSCIdentityFn
	}
	readers, err := listPCSCReadersFn(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PCSCReaderDevice, len(readers))
	var wg sync.WaitGroup
	for i, reader := range readers {
		wg.Add(1)
		go func(index int, readerName string) {
			defer wg.Done()
			readerName = strings.TrimSpace(readerName)
			item := PCSCReaderDevice{ReaderName: readerName}
			probeCtx, cancel := context.WithTimeout(ctx, pcscProbeTimeout)
			defer cancel()
			identity, probeErr := probe(probeCtx, readerName)
			if probeErr != nil {
				item.Error = probeErr.Error()
				out[index] = item
				return
			}
			item.ICCID = strings.TrimSpace(identity.ICCID)
			item.IMSI = strings.TrimSpace(identity.IMSI)
			item.MCC = strings.TrimSpace(identity.MCC)
			item.MNC = strings.TrimSpace(identity.MNC)
			out[index] = item
		}(i, reader)
	}
	wg.Wait()
	return out, nil
}

// DiscoverPCSCReaders reuses an active worker's cached card identity while its
// APDU channel is busy. This prevents a manual scan or the hot-plug loop from
// interrupting eSIM downloads and VoWiFi AKA on the same physical reader.
func (p *Pool) DiscoverPCSCReaders(ctx context.Context) ([]PCSCReaderDevice, error) {
	if p == nil {
		return DiscoverPCSCReaders(ctx)
	}
	workersByReader := make(map[string]*Worker)
	for _, worker := range p.GetAllWorkers() {
		if worker == nil || !strings.EqualFold(workerBackendMode(worker), backend.BackendPCSC) {
			continue
		}
		if readerName := strings.TrimSpace(worker.Config.ReaderName); readerName != "" {
			workersByReader[readerName] = worker
		}
	}
	return discoverPCSCReadersWithProbe(ctx, func(probeCtx context.Context, readerName string) (backend.PCSCIdentity, error) {
		worker := workersByReader[strings.TrimSpace(readerName)]
		if worker == nil {
			return probePCSCIdentityFn(probeCtx, readerName)
		}
		pcscBackend, ok := worker.Backend.(*backend.PCSCBackend)
		if !ok {
			return probePCSCIdentityFn(probeCtx, readerName)
		}
		if worker.APDUArbiter != nil && !worker.APDUArbiter.IsIdle() {
			if cached, ok := pcscBackend.CachedIdentity(); ok {
				return cached, nil
			}
		}
		return pcscBackend.Identity(probeCtx, true)
	})
}
