package db

import (
	"strings"
	"time"
)

type VoWiFiConnectionEvent struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	DeviceID       string    `gorm:"index" json:"device_id"`
	ICCID          string    `gorm:"index" json:"iccid,omitempty"`
	Phase          string    `gorm:"index" json:"phase"`
	Stage          string    `gorm:"index" json:"stage"`
	SIMReady       bool      `json:"sim_ready"`
	AccessReady    bool      `json:"access_ready"`
	TunnelReady    bool      `json:"tunnel_ready"`
	IMSReady       bool      `json:"ims_ready"`
	SMSReady       bool      `json:"sms_ready"`
	ErrorClass     string    `json:"error_class,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	Detail         string    `json:"detail,omitempty"`
	UpstreamProxyID string   `gorm:"index" json:"upstream_proxy_id,omitempty"`
	TraceID        string    `gorm:"index" json:"trace_id,omitempty"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

func (VoWiFiConnectionEvent) TableName() string { return "vowifi_connection_events" }

type VoWiFiHistorySummary struct {
	DeviceID           string                  `json:"device_id"`
	WindowDays         int                     `json:"window_days"`
	Availability       float64                 `json:"availability_percent"`
	ReadySeconds       int64                   `json:"ready_seconds"`
	Successes          int                     `json:"successes"`
	Failures           int                     `json:"failures"`
	LastFailure        *VoWiFiConnectionEvent  `json:"last_failure,omitempty"`
	Events             []VoWiFiConnectionEvent `json:"events"`
}

func RecordVoWiFiConnectionEvent(event VoWiFiConnectionEvent) error {
	if DB == nil {
		return nil
	}
	event.DeviceID = strings.TrimSpace(event.DeviceID)
	if event.DeviceID == "" {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	var previous VoWiFiConnectionEvent
	err := DB.Where("device_id = ?", event.DeviceID).Order("created_at desc, id desc").First(&previous).Error
	if err == nil && previous.Phase == event.Phase && previous.Stage == event.Stage &&
		previous.ErrorClass == event.ErrorClass && previous.Reason == event.Reason &&
		previous.SMSReady == event.SMSReady && event.CreatedAt.Sub(previous.CreatedAt) >= 0 && event.CreatedAt.Sub(previous.CreatedAt) < 2*time.Second {
		return nil
	}
	return DB.Create(&event).Error
}

func GetVoWiFiHistory(deviceID string, limit, days int) (VoWiFiHistorySummary, error) {
	deviceID = strings.TrimSpace(deviceID)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if days <= 0 || days > 90 {
		days = 7
	}
	if DB == nil {
		return VoWiFiHistorySummary{DeviceID: deviceID, WindowDays: days, Events: []VoWiFiConnectionEvent{}}, nil
	}
	now := time.Now()
	windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)
	var chronological []VoWiFiConnectionEvent
	if err := DB.Where("device_id = ? AND created_at >= ?", deviceID, windowStart).
		Order("created_at asc, id asc").Find(&chronological).Error; err != nil {
		return VoWiFiHistorySummary{}, err
	}

	summary := VoWiFiHistorySummary{DeviceID: deviceID, WindowDays: days}
	var readySince *time.Time
	var beforeWindow VoWiFiConnectionEvent
	if err := DB.Where("device_id = ? AND created_at < ?", deviceID, windowStart).
		Order("created_at desc, id desc").First(&beforeWindow).Error; err == nil && beforeWindow.SMSReady {
		start := windowStart
		readySince = &start
	}
	for i := range chronological {
		event := chronological[i]
		if event.SMSReady && readySince == nil {
			start := event.CreatedAt
			readySince = &start
			summary.Successes++
		}
		if !event.SMSReady && readySince != nil {
			summary.ReadySeconds += int64(event.CreatedAt.Sub(*readySince).Seconds())
			readySince = nil
		}
		if event.Phase == "failed" {
			summary.Failures++
			copy := event
			summary.LastFailure = &copy
		}
	}
	if readySince != nil {
		summary.ReadySeconds += int64(now.Sub(*readySince).Seconds())
	}
	windowSeconds := int64(now.Sub(windowStart).Seconds())
	if windowSeconds > 0 {
		summary.Availability = float64(summary.ReadySeconds) * 100 / float64(windowSeconds)
		if summary.Availability > 100 {
			summary.Availability = 100
		}
	}

	if err := DB.Where("device_id = ?", deviceID).Order("created_at desc, id desc").Limit(limit).Find(&summary.Events).Error; err != nil {
		return VoWiFiHistorySummary{}, err
	}
	return summary, nil
}
