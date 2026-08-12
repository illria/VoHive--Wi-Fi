package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestVoWiFiHistoryReportsFailureStageAndAvailability(t *testing.T) {
	if err := Init(filepath.Join(t.TempDir(), "history.db")); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	now := time.Now()
	events := []VoWiFiConnectionEvent{
		{DeviceID: "dev-1", Phase: "sms_ready", Stage: "SMS", SMSReady: true, CreatedAt: now.Add(-2 * time.Hour)},
		{DeviceID: "dev-1", Phase: "failed", Stage: "IMS", SMSReady: false, ErrorClass: "ims", Reason: "registration timeout", CreatedAt: now.Add(-time.Hour)},
	}
	for _, event := range events {
		if err := RecordVoWiFiConnectionEvent(event); err != nil {
			t.Fatalf("RecordVoWiFiConnectionEvent() error=%v", err)
		}
	}
	summary, err := GetVoWiFiHistory("dev-1", 10, 1)
	if err != nil {
		t.Fatalf("GetVoWiFiHistory() error=%v", err)
	}
	if summary.Successes != 1 || summary.Failures != 1 || summary.LastFailure == nil || summary.LastFailure.Stage != "IMS" {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Availability <= 3.5 || summary.Availability >= 5.0 {
		t.Fatalf("availability=%f, want about 4.17", summary.Availability)
	}
}
