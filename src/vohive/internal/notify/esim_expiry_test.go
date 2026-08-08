package notify

import (
	"testing"
	"time"
)

func TestEsimExpiryNoticeDueWithinSevenDays(t *testing.T) {
	now := time.Date(2026, time.August, 8, 16, 30, 0, 0, time.Local)

	tests := []struct {
		name       string
		expiryDate string
		noticeDate string
		wantDue    bool
		wantDays   int
	}{
		{name: "seven days", expiryDate: "2026-08-15", wantDue: true, wantDays: 7},
		{name: "today", expiryDate: "2026-08-08", wantDue: true, wantDays: 0},
		{name: "outside window", expiryDate: "2026-08-16", wantDue: false, wantDays: 8},
		{name: "expired", expiryDate: "2026-08-07", wantDue: false, wantDays: -1},
		{name: "already notified", expiryDate: "2026-08-10", noticeDate: "2026-08-10", wantDue: false, wantDays: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			due, days, err := esimExpiryNoticeDue(tt.expiryDate, tt.noticeDate, now)
			if err != nil {
				t.Fatalf("esimExpiryNoticeDue() error=%v", err)
			}
			if due != tt.wantDue || days != tt.wantDays {
				t.Fatalf("esimExpiryNoticeDue() = due:%v days:%d, want due:%v days:%d", due, days, tt.wantDue, tt.wantDays)
			}
		})
	}
}

func TestEsimExpiryNoticeDueRejectsInvalidDate(t *testing.T) {
	_, _, err := esimExpiryNoticeDue("2026/08/15", "", time.Now())
	if err == nil {
		t.Fatal("invalid expiry date should return an error")
	}
}
