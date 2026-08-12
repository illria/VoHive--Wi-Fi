package device

import (
	"testing"

	"github.com/iniwex5/vowifi-go/runtimehost"
)

func TestVoWiFiFailureStageReportsFirstUnreadyBoundary(t *testing.T) {
	tests := []struct {
		name  string
		state runtimehost.State
		want  string
	}{
		{name: "sim", state: runtimehost.State{Phase: runtimehost.PhaseFailed}, want: "SIM"},
		{name: "access", state: runtimehost.State{Phase: runtimehost.PhaseFailed, SIMReady: true}, want: "Access"},
		{name: "tunnel", state: runtimehost.State{Phase: runtimehost.PhaseFailed, SIMReady: true, AccessReady: true}, want: "Tunnel"},
		{name: "ims", state: runtimehost.State{Phase: runtimehost.PhaseFailed, SIMReady: true, AccessReady: true, TunnelReady: true}, want: "IMS"},
		{name: "sms", state: runtimehost.State{Phase: runtimehost.PhaseFailed, SIMReady: true, AccessReady: true, TunnelReady: true, IMSReady: true}, want: "SMS"},
		{name: "runtime", state: runtimehost.State{Phase: runtimehost.PhaseFailed, SIMReady: true, AccessReady: true, TunnelReady: true, IMSReady: true, SMSReady: true}, want: "Runtime"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := voWiFiFailureStage(tc.state); got != tc.want {
				t.Fatalf("voWiFiFailureStage() = %q, want %q", got, tc.want)
			}
		})
	}
}
