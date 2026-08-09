package runtimehost

import (
	"context"
	"net"
	"testing"

	swusim "github.com/iniwex5/vowifi-go/engine/sim"
	vowifimodel "github.com/iniwex5/vowifi-go/internal/vowifi"
	"github.com/iniwex5/vowifi-go/internal/vowifi/runtimecore"
)

type imsRuntimeTestSIM struct{}

func (imsRuntimeTestSIM) GetIMSI() (string, error) { return "204040000000001", nil }

func (imsRuntimeTestSIM) CalculateAKA(rand, autn []byte) (swusim.AKAResult, error) {
	return swusim.AKAResult{
		RES: append([]byte(nil), rand...),
		CK:  append([]byte(nil), autn...),
		IK:  []byte{0x01, 0x02},
	}, nil
}

func (imsRuntimeTestSIM) Close() error { return nil }

func TestRuntimeAKAProviderCopiesSIMResult(t *testing.T) {
	provider := runtimeAKAProvider{sim: imsRuntimeTestSIM{}}
	var challenge vowifimodel.AKAChallenge
	challenge.RAND[0] = 0x10
	challenge.AUTN[0] = 0x20
	result, err := provider.Authenticate(context.Background(), vowifimodel.SIMIdentity{IMSI: "204040000000001"}, challenge)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if len(result.RES) != 16 || result.RES[0] != 0x10 || len(result.CK) != 16 || result.CK[0] != 0x20 {
		t.Fatalf("unexpected AKA result: %+v", result)
	}
	if evidence, err := provider.CheckReady(context.Background(), vowifimodel.SIMIdentity{IMSI: "204040000000001"}); err != nil || !evidence.Ready {
		t.Fatalf("CheckReady() = %+v, %v", evidence, err)
	}
}

func TestIMSTunnelEvidenceUsesAssignedAddressesAndPCSCF(t *testing.T) {
	evidence := (imsTunnelEvidence{snapshot: runtimecore.TunnelSnapshot{
		Established: true,
		TUNName:     "vowifi0",
		IPv4:        net.ParseIP("10.0.0.2"),
		IPv6:        net.ParseIP("2001:db8::2"),
		PCSCFv4:     []net.IP{net.ParseIP("10.0.0.1")},
		PCSCFv6:     []net.IP{net.ParseIP("2001:db8::1")},
	}}).Evidence()
	if !evidence.Established || evidence.Name != "vowifi0" || evidence.LocalIPv4 != "10.0.0.2" || evidence.LocalIPv6 != "2001:db8::2" {
		t.Fatalf("unexpected tunnel evidence: %+v", evidence)
	}
	if len(evidence.PCSCF) != 2 || evidence.PCSCF[0] != "10.0.0.1" || evidence.PCSCF[1] != "2001:db8::1" {
		t.Fatalf("unexpected P-CSCF evidence: %+v", evidence.PCSCF)
	}
}
