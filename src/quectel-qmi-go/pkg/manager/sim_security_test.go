package manager

import (
	"context"
	"errors"
	"testing"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

func simSecurityCardStatusPacket(state qmi.PINStatus, retries uint8) *qmi.Packet {
	aid := []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}
	value := make([]byte, 15, 15+14+len(aid))
	value[8] = 1
	value[9] = 0x01
	value[10] = byte(state)
	value[11] = retries
	value[12] = 10
	value[14] = 1
	value = append(value,
		qmi.UIMAppTypeUSIM, 0x01, 0x00, 0x00, 0x00, 0x00, byte(len(aid)))
	value = append(value, aid...)
	value = append(value,
		0x01, // application uses UPIN
		byte(qmi.PINStatusDisabled), 0x03, 0x0A,
		byte(qmi.PINStatusDisabled), 0x03, 0x0A,
	)
	return &qmi.Packet{TLVs: []qmi.TLV{
		{Type: 0x02, Value: []byte{0x00, 0x00, 0x00, 0x00}},
		{Type: 0x10, Value: value},
	}}
}

func TestVerifySIMPINUsesUPINAndReadsFinalState(t *testing.T) {
	var requests int
	m := newRecoveryTestManager()
	m.ensureUIMServiceHook = func() (*qmi.UIMService, error) {
		return newUIMReadinessTestService(t, func(req *qmi.Packet) (*qmi.Packet, error) {
			requests++
			switch requests {
			case 1:
				if req.MessageID != qmi.UIMGetCardStatus {
					return nil, errors.New("expected initial card status")
				}
				return simSecurityCardStatusPacket(qmi.PINStatusNotVerified, 3), nil
			case 2:
				if req.MessageID != qmi.UIMVerifyPIN {
					return nil, errors.New("expected one VerifyPIN request")
				}
				pinInfo := qmi.FindTLV(req.TLVs, 0x01)
				if pinInfo == nil || len(pinInfo.Value) < 2 || pinInfo.Value[0] != qmi.UIMPinIDUPIN {
					return nil, errors.New("VerifyPIN did not use UPIN ID")
				}
				return &qmi.Packet{TLVs: []qmi.TLV{{Type: 0x02, Value: []byte{0, 0, 0, 0}}}}, nil
			case 3:
				if req.MessageID != qmi.UIMGetCardStatus {
					return nil, errors.New("expected final card status")
				}
				return simSecurityCardStatusPacket(qmi.PINStatusVerified, 2), nil
			default:
				return nil, errors.New("unexpected extra QMI request")
			}
		}), nil
	}

	details, status, err := m.VerifySIMPIN(context.Background(), "1234")
	if err != nil {
		t.Fatalf("VerifySIMPIN() error = %v", err)
	}
	if status != qmi.SIMReady || details == nil || !details.UsesUPIN || details.UPINRetries != 2 {
		t.Fatalf("final state = details=%+v status=%v, want ready/UPIN/2 retries", details, status)
	}
	if requests != 3 {
		t.Fatalf("QMI request count = %d, want exactly initial status + VerifyPIN + final status", requests)
	}
}

func TestVerifySIMPINDoesNotSendWhenNoRetriesRemain(t *testing.T) {
	m := newRecoveryTestManager()
	m.ensureUIMServiceHook = func() (*qmi.UIMService, error) {
		return newUIMReadinessTestService(t, func(req *qmi.Packet) (*qmi.Packet, error) {
			if req.MessageID != qmi.UIMGetCardStatus {
				return nil, errors.New("VerifyPIN should not be sent when retries are exhausted")
			}
			return simSecurityCardStatusPacket(qmi.PINStatusNotVerified, 0), nil
		}), nil
	}

	details, status, err := m.VerifySIMPIN(context.Background(), "1234")
	if !errors.Is(err, ErrSIMPINNoRetries) {
		t.Fatalf("VerifySIMPIN() error = %v, want ErrSIMPINNoRetries", err)
	}
	if details == nil || status != qmi.SIMPINRequired {
		t.Fatalf("state = details=%+v status=%v, want PIN required", details, status)
	}
}
