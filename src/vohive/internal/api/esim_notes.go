package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iniwex5/vohive/internal/db"
	"github.com/iniwex5/vohive/internal/esim"
	"github.com/gin-gonic/gin"
)

const maxEsimProfileNoteRunes = 500

// enrichEsimProfileMetadata adds application-owned metadata without changing
// the profile data stored on the eUICC itself. Database read failures are
// ignored here so optional UI metadata can never make the eSIM page unavailable.
func enrichEsimProfileMetadata(groups []esim.EUICCProfiles) {
	iccids := make([]string, 0)
	for _, group := range groups {
		for _, profile := range group.Profiles {
			if iccid := strings.TrimSpace(profile.ICCID); iccid != "" {
				iccids = append(iccids, iccid)
			}
		}
	}
	notes, err := db.GetEsimProfileNotes(iccids)
	expiryDates, expiryErr := db.GetEsimProfileExpiryDates(iccids)
	for groupIndex := range groups {
		for profileIndex := range groups[groupIndex].Profiles {
			iccid := strings.TrimSpace(groups[groupIndex].Profiles[profileIndex].ICCID)
			if err == nil {
				groups[groupIndex].Profiles[profileIndex].Note = notes[iccid]
			}
			if expiryErr == nil {
				groups[groupIndex].Profiles[profileIndex].ExpiryDate = expiryDates[iccid]
			}
			if phone, phoneErr := db.GetPhoneNumberByICCID(iccid); phoneErr == nil {
				groups[groupIndex].Profiles[profileIndex].PhoneNumber = phone
			}
		}
	}
}

// enrichEsimProfileNotes is kept as a small compatibility wrapper for callers
// outside this file while the response now also carries expiry metadata.
func enrichEsimProfileNotes(groups []esim.EUICCProfiles) {
	enrichEsimProfileMetadata(groups)
}

func (s *Server) handleEsimProfileNote(c *gin.Context) {
	id := deviceIDParam(c)
	iccid := strings.TrimSpace(c.Param("iccid"))
	worker := s.pool.GetWorker(id)
	if worker == nil || worker.EsimMgr == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备或esim管理器未找到"})
		return
	}
	if iccid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "iccid 为必填项"})
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "note 参数格式无效"})
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(req.Note) > maxEsimProfileNoteRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "备注不能超过 500 个字符"})
		return
	}
	if err := db.UpsertEsimProfileNote(iccid, req.Note); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 eSIM 备注失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "iccid": iccid, "note": req.Note})
}

func (s *Server) handleEsimProfileExpiry(c *gin.Context) {
	id := deviceIDParam(c)
	iccid := strings.TrimSpace(c.Param("iccid"))
	worker := s.pool.GetWorker(id)
	if worker == nil || worker.EsimMgr == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "设备或esim管理器未找到"})
		return
	}
	if iccid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "iccid 为必填项"})
		return
	}

	var req struct {
		ExpiryDate string `json:"expiry_date"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expiry_date 参数格式无效"})
		return
	}
	normalized, err := db.NormalizeEsimProfileExpiryDate(req.ExpiryDate)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if normalized != "" {
		if parsed, parseErr := time.ParseInLocation(db.EsimProfileExpiryDateLayout, normalized, time.Local); parseErr != nil || parsed.IsZero() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expiry_date 参数无效"})
			return
		}
	}
	if err := db.UpsertEsimProfileExpiry(iccid, normalized); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存 eSIM 到期日期失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "iccid": iccid, "expiry_date": normalized})
}
