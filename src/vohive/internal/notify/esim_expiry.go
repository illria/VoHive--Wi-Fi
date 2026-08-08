package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/vohive/internal/db"
	"github.com/iniwex5/vohive/pkg/logger"
)

const esimExpiryNoticeLeadDays = 7

// StartEsimExpiryScheduler starts the persistent eSIM expiry reminder loop.
// It checks once immediately and then once per day. The reminder marker is
// stored with the expiry date, so each configured date produces at most one
// notification even if the process restarts inside the seven-day window.
func (m *Manager) StartEsimExpiryScheduler(ctx context.Context) {
	if m == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		m.checkEsimProfileExpiry(time.Now())
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				m.checkEsimProfileExpiry(now)
			}
		}
	}()
}

func (m *Manager) checkEsimProfileExpiry(now time.Time) {
	if len(m.channels) == 0 {
		return
	}

	candidates, err := db.ListEsimProfileExpiryCandidates()
	if err != nil {
		logger.Warn("读取 eSIM 到期提醒列表失败", "err", err)
		return
	}
	for _, candidate := range candidates {
		due, daysRemaining, dueErr := esimExpiryNoticeDue(candidate.ExpiryDate, candidate.ExpiryNoticeDate, now)
		if dueErr != nil {
			logger.Warn("跳过无效的 eSIM 到期日期", "iccid", candidate.ICCID, "expiry_date", candidate.ExpiryDate, "err", dueErr)
			continue
		}
		if !due {
			continue
		}
		if err := db.MarkEsimProfileExpiryNotified(candidate.ICCID, candidate.ExpiryDate); err != nil {
			logger.Warn("记录 eSIM 到期提醒状态失败", "iccid", candidate.ICCID, "err", err)
			continue
		}

		phone := strings.TrimSpace(candidate.PhoneNumber)
		if phone == "" {
			phone = "未读取到号码"
		}
		message := fmt.Sprintf(
			"eSIM 到期续费提醒\n号码    %s\nICCID   %s\n到期日  %s\n剩余    %d 天\n请及时续费",
			phone,
			candidate.ICCID,
			candidate.ExpiryDate,
			daysRemaining,
		)
		if note := strings.TrimSpace(candidate.Note); note != "" {
			message += "\n备注    " + note
		}
		m.NotifyRaw(message)
	}
}

func esimExpiryNoticeDue(expiryDate, noticeDate string, now time.Time) (bool, int, error) {
	expiryDate = strings.TrimSpace(expiryDate)
	if expiryDate == "" || strings.TrimSpace(noticeDate) == expiryDate {
		return false, 0, nil
	}

	parsed, err := time.ParseInLocation(db.EsimProfileExpiryDateLayout, expiryDate, now.Location())
	if err != nil {
		return false, 0, err
	}
	dateOnly := func(value time.Time) time.Time {
		return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
	}
	daysRemaining := int(dateOnly(parsed).Sub(dateOnly(now)).Hours() / 24)
	return daysRemaining >= 0 && daysRemaining <= esimExpiryNoticeLeadDays, daysRemaining, nil
}
