package db

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

const EsimProfileExpiryDateLayout = "2006-01-02"

// EsimProfileNote stores application metadata for an eSIM profile. The note is
// keyed by ICCID so it follows the profile when profiles are switched. Expiry
// metadata intentionally lives in the same row so an existing database can be
// upgraded by GORM without a second metadata table.
type EsimProfileNote struct {
	ICCID            string    `gorm:"column:iccid;primaryKey" json:"iccid"`
	Note             string    `gorm:"column:note" json:"note"`
	ExpiryDate       string    `gorm:"column:expiry_date" json:"expiry_date"`
	ExpiryNoticeDate string    `gorm:"column:expiry_notice_date" json:"-"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (EsimProfileNote) TableName() string { return "esim_profile_notes" }

// EsimProfileExpiryCandidate is the persisted data needed by the daily
// expiry-reminder scheduler. PhoneNumber may be empty when the modem has not
// reported a number yet; callers should then use ICCID as the stable label.
type EsimProfileExpiryCandidate struct {
	ICCID            string
	Note             string
	ExpiryDate       string
	ExpiryNoticeDate string
	PhoneNumber      string
}

// NormalizeEsimProfileExpiryDate validates and normalizes a date-only value.
// Date-only storage avoids timezone-dependent reminders around midnight.
func NormalizeEsimProfileExpiryDate(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := time.ParseInLocation(EsimProfileExpiryDateLayout, raw, time.Local)
	if err != nil {
		return "", errors.New("到期日期格式必须为 YYYY-MM-DD")
	}
	return parsed.Format(EsimProfileExpiryDateLayout), nil
}

// GetEsimProfileNote returns an empty note when the profile has no saved note.
func GetEsimProfileNote(iccid string) (string, error) {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return "", nil
	}
	if DB == nil {
		return "", errors.New("db 未初始化")
	}

	var row EsimProfileNote
	err := DB.Where("iccid = ?", iccid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.Note, nil
}

// GetEsimProfileNotes returns notes for the requested ICCIDs in one query.
func GetEsimProfileNotes(iccidList []string) (map[string]string, error) {
	result := make(map[string]string)
	if DB == nil {
		return result, errors.New("db 未初始化")
	}

	ids := make([]string, 0, len(iccidList))
	seen := make(map[string]struct{}, len(iccidList))
	for _, raw := range iccidList {
		iccid := strings.TrimSpace(raw)
		if iccid == "" {
			continue
		}
		if _, ok := seen[iccid]; ok {
			continue
		}
		seen[iccid] = struct{}{}
		ids = append(ids, iccid)
	}
	if len(ids) == 0 {
		return result, nil
	}

	var rows []EsimProfileNote
	if err := DB.Where("iccid IN ?", ids).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		result[row.ICCID] = row.Note
	}
	return result, nil
}

// GetEsimProfileExpiryDates returns saved expiry dates for the requested ICCIDs.
func GetEsimProfileExpiryDates(iccidList []string) (map[string]string, error) {
	result := make(map[string]string)
	if DB == nil {
		return result, errors.New("db 未初始化")
	}

	ids := make([]string, 0, len(iccidList))
	seen := make(map[string]struct{}, len(iccidList))
	for _, raw := range iccidList {
		iccid := strings.TrimSpace(raw)
		if iccid == "" {
			continue
		}
		if _, ok := seen[iccid]; ok {
			continue
		}
		seen[iccid] = struct{}{}
		ids = append(ids, iccid)
	}
	if len(ids) == 0 {
		return result, nil
	}

	var rows []EsimProfileNote
	if err := DB.Select("iccid", "expiry_date").Where("iccid IN ?", ids).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		if expiryDate := strings.TrimSpace(row.ExpiryDate); expiryDate != "" {
			result[row.ICCID] = expiryDate
		}
	}
	return result, nil
}

// UpsertEsimProfileNote saves or clears a profile note.
func UpsertEsimProfileNote(iccid, note string) error {
	iccid = strings.TrimSpace(iccid)
	note = strings.TrimSpace(note)
	if iccid == "" {
		return errors.New("ICCID 为空")
	}
	if DB == nil {
		return errors.New("db 未初始化")
	}

	var existing EsimProfileNote
	err := DB.Where("iccid = ?", iccid).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if note == "" {
			return nil
		}
		now := time.Now()
		return DB.Create(&EsimProfileNote{
			ICCID:     iccid,
			Note:      note,
			CreatedAt: now,
			UpdatedAt: now,
		}).Error
	}

	if note == "" && strings.TrimSpace(existing.ExpiryDate) == "" {
		return DB.Delete(&existing).Error
	}
	return DB.Model(&existing).Updates(map[string]any{
		"note":       note,
		"updated_at": time.Now(),
	}).Error
}

// UpsertEsimProfileExpiry saves, changes, or clears a profile expiry date.
// Changing the date resets the one-shot reminder marker for the new date.
func UpsertEsimProfileExpiry(iccid, expiryDate string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" {
		return errors.New("ICCID 为空")
	}
	if DB == nil {
		return errors.New("db 未初始化")
	}
	normalized, err := NormalizeEsimProfileExpiryDate(expiryDate)
	if err != nil {
		return err
	}

	var existing EsimProfileNote
	err = DB.Where("iccid = ?", iccid).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if normalized == "" {
			return nil
		}
		now := time.Now()
		return DB.Create(&EsimProfileNote{
			ICCID:      iccid,
			ExpiryDate: normalized,
			CreatedAt:  now,
			UpdatedAt:  now,
		}).Error
	}

	updates := map[string]any{
		"expiry_date": normalized,
		"updated_at": time.Now(),
	}
	if normalized == "" {
		updates["expiry_notice_date"] = ""
	}
	if normalized != strings.TrimSpace(existing.ExpiryDate) {
		updates["expiry_notice_date"] = ""
	}
	if normalized == "" && strings.TrimSpace(existing.Note) == "" {
		return DB.Delete(&existing).Error
	}
	return DB.Model(&existing).Updates(updates).Error
}

// ListEsimProfileExpiryCandidates returns profiles with a configured expiry.
func ListEsimProfileExpiryCandidates() ([]EsimProfileExpiryCandidate, error) {
	if DB == nil {
		return nil, errors.New("db 未初始化")
	}
	var rows []EsimProfileNote
	if err := DB.Where("COALESCE(expiry_date, '') <> ''").Find(&rows).Error; err != nil {
		return nil, err
	}

	candidates := make([]EsimProfileExpiryCandidate, 0, len(rows))
	for _, row := range rows {
		phone, err := GetPhoneNumberByICCID(row.ICCID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, EsimProfileExpiryCandidate{
			ICCID:            row.ICCID,
			Note:             row.Note,
			ExpiryDate:       row.ExpiryDate,
			ExpiryNoticeDate: row.ExpiryNoticeDate,
			PhoneNumber:      phone,
		})
	}
	return candidates, nil
}

// MarkEsimProfileExpiryNotified records the date for which a reminder was
// sent. The expiry-date predicate prevents an old scheduler tick from marking
// a newly edited date.
func MarkEsimProfileExpiryNotified(iccid, expiryDate string) error {
	iccid = strings.TrimSpace(iccid)
	expiryDate = strings.TrimSpace(expiryDate)
	if iccid == "" || expiryDate == "" || DB == nil {
		return nil
	}
	return DB.Model(&EsimProfileNote{}).
		Where("iccid = ? AND expiry_date = ?", iccid, expiryDate).
		Updates(map[string]any{
			"expiry_notice_date": expiryDate,
			"updated_at":         time.Now(),
		}).Error
}

func DeleteEsimProfileNote(iccid string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" || DB == nil {
		return nil
	}
	return DB.Delete(&EsimProfileNote{}, "iccid = ?", iccid).Error
}
