package qmi

import (
	"context"
	"testing"
)

func TestUIMPINIDsUseQMIEnumValues(t *testing.T) {
	if UIMPinIDPIN1 != 1 || UIMPinIDPIN2 != 2 || UIMPinIDUPIN != 3 {
		t.Fatalf("PIN IDs = (%d, %d, %d), want (1, 2, 3)", UIMPinIDPIN1, UIMPinIDPIN2, UIMPinIDUPIN)
	}
}

func TestUIMServiceVerifyPINEncodesRequestedPINID(t *testing.T) {
	client := newUIMUnitTestClient()
	stop := serveUIMUnitTestRequests(t, client, func(req *Packet) *Packet {
		if req.MessageID != UIMVerifyPIN {
			t.Fatalf("message id = 0x%04x, want UIMVerifyPIN", req.MessageID)
		}
		tlv := FindTLV(req.TLVs, 0x01)
		if tlv == nil || len(tlv.Value) < 2 {
			t.Fatalf("missing PIN info TLV")
		}
		if tlv.Value[0] != UIMPinIDUPIN {
			t.Fatalf("encoded PIN ID = %d, want UPIN ID %d", tlv.Value[0], UIMPinIDUPIN)
		}
		if tlv.Value[1] != 4 {
			t.Fatalf("encoded PIN length = %d, want 4", tlv.Value[1])
		}
		return &Packet{TLVs: []TLV{successResultTLV()}}
	})
	defer stop()

	uim := &UIMService{client: client, clientID: 1}
	if err := uim.VerifyPIN(context.Background(), UIMPinIDUPIN, "1234"); err != nil {
		t.Fatalf("VerifyPIN() error = %v", err)
	}
}
