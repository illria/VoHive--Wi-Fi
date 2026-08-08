package db

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EsimProfileNote stores application metadata for an eSIM profile. The note is
// keyed by ICCID so it follows the profile when profiles are switched.
type EsimProfileNote struct {
	ICCID     string    `gorm:"column:iccid;primaryKey" json:"iccid"`
	Note      string    `gorm:"column:note" json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (EsimProfileNote) TableName() string { return "esim_profile_notes" }

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
	if note == "" {
		return DB.Delete(&EsimProfileNote{}, "iccid = ?", iccid).Error
	}

	now := time.Now()
	row := EsimProfileNote{
		ICCID:     iccid,
		Note:      note,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "iccid"}},
		DoUpdates: clause.Assignments(map[string]any{
			"note":       note,
			"updated_at": now,
		}),
	}).Create(&row).Error
}

func DeleteEsimProfileNote(iccid string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" || DB == nil {
		return nil
	}
	return DB.Delete(&EsimProfileNote{}, "iccid = ?", iccid).Error
}
