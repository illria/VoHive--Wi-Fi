package uim

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/damonto/uicc-go/qcom"
)

const (
	DefaultRequestTimeout = 30 * time.Second
	defaultCloseTimeout   = 5 * time.Second
)

type Reader struct {
	mu          sync.Mutex
	transport   qcom.Transport
	slot        uint8
	clientID    uint8
	catClientID uint8
	catService  qcom.ServiceType
	txn         uint16
	ctlTxn      uint8
	closeOnce   sync.Once
	closed      bool
	closeErr    error
}

type Option func(*config)

type config struct {
	slot uint8
}

type serviceBoundTransport interface {
	QMIService() qcom.ServiceType
}

func WithSlot(slot uint8) Option {
	return func(c *config) {
		c.slot = slot
	}
}

func New(ctx context.Context, transport qcom.Transport, opts ...Option) (*Reader, error) {
	if transport == nil {
		return nil, errors.New("creating QMI UIM client: transport is nil")
	}

	cfg := config{slot: 1}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.slot < 1 || cfg.slot > 5 {
		return nil, fmt.Errorf("creating QMI UIM client: slot %d is out of range", cfg.slot)
	}

	reader := &Reader{
		transport: transport,
		slot:      cfg.slot,
	}
	if service, ok := boundQMIService(transport); ok {
		if service != qcom.ServiceUIM {
			_ = transport.Close()
			return nil, fmt.Errorf("creating QMI UIM client: transport is bound to service 0x%02X, want UIM service 0x%02X", service, qcom.ServiceUIM)
		}
		return reader, nil
	}
	if err := reader.allocateClientID(ctx); err != nil {
		_ = transport.Close()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("creating QMI UIM client: transport closed while allocating client ID")
		}
		return nil, fmt.Errorf("creating QMI UIM client: %w", err)
	}
	return reader, nil
}

func (r *Reader) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCloseTimeout)
	defer cancel()
	r.closeOnce.Do(func() {
		r.mu.Lock()
		defer r.mu.Unlock()

		transport := r.transport
		if transport == nil {
			r.closed = true
			r.clientID = 0
			return
		}

		var releaseErr error
		_, serviceBound := boundQMIService(transport)
		if r.catClientID != 0 {
			if !serviceBound {
				releaseErr = r.releaseServiceClientID(ctx, r.catService, r.catClientID)
			}
			r.catClientID = 0
			r.catService = 0
		}
		if r.clientID != 0 {
			if !serviceBound {
				releaseErr = errors.Join(releaseErr, r.releaseServiceClientID(ctx, qcom.ServiceUIM, r.clientID))
			}
			r.clientID = 0
		}

		closeErr := transport.Close()
		r.transport = nil
		r.closed = true
		if releaseErr == nil {
			r.closeErr = closeErr
			return
		}
		r.closeErr = errors.Join(releaseErr, closeErr)
	})
	return r.closeErr
}

func boundQMIService(transport qcom.Transport) (qcom.ServiceType, bool) {
	bound, ok := transport.(serviceBoundTransport)
	if !ok {
		return 0, false
	}
	return bound.QMIService(), true
}

func (r *Reader) nextTransactionID(service qcom.ServiceType) uint16 {
	if service == qcom.ServiceControl {
		r.ctlTxn++
		if r.ctlTxn == 0 {
			r.ctlTxn++
		}
		return uint16(r.ctlTxn)
	}

	r.txn++
	if r.txn == 0 {
		r.txn++
	}
	return r.txn
}
