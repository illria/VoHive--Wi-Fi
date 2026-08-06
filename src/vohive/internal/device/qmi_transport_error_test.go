package device

import "testing"

func TestQMIErrorIndicatesTransportDown(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"broken_pipe", "write failed: write unix @->@qmi-proxy: write: broken pipe", true},
		{"eof", "QMI: read failed: EOF", true},
		{"io_error", "read failed: input/output error", true},
		{"device_node_missing", "control device node missing", true},
		{"deadline", "context deadline exceeded", false},
		{"connection_closed", "connection closed", true},
		{"no_such_device", "open /dev/cdc-wdm2: no such device", true},
		{"failed_open", "failed to open qmi device", true},
		{"empty", "", false},
		{"identity_empty", "refresh_identity: live_identity_empty", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := qmiErrorIndicatesTransportDown(tc.msg); got != tc.want {
				t.Fatalf("qmiErrorIndicatesTransportDown(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestQMIErrorIndicatesIdentityPending(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  string
		want bool
	}{
		{name: "live_empty", msg: "refresh_identity: live_identity_empty", want: true},
		{name: "not_readable", msg: "identity not readable before deadline", want: true},
		{name: "deadline", msg: "refresh_identity: context deadline exceeded", want: true},
		{name: "broken_pipe", msg: "refresh_identity: write failed: broken pipe", want: false},
		{name: "empty", msg: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := qmiErrorIndicatesIdentityPending(tc.msg); got != tc.want {
				t.Fatalf("qmiErrorIndicatesIdentityPending(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
