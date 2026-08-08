package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/iniwex5/vohive/internal/db"
	"github.com/iniwex5/vohive/internal/esim"
	"github.com/gin-gonic/gin"
)

const maxEsimProfileNoteRunes = 500

// enrichEsimProfileNotes adds application-owned notes without changing the
// profile data stored on the eUICC itself. A database read failure is ignored
// here so an optional note can never make the eSIM page unavailable.
func enrichEsimProfileNotes(groups []esim.EUICCProfiles) {
	iccids := make([]string, 0)
	for _, group := range groups {
		for _, profile := range group.Profiles {
			if iccid := strings.TrimSpace(profile.ICCID); iccid != "" {
				iccids = append(iccids, iccid)
			}
		}
	}
	notes, err := db.GetEsimProfileNotes(iccids)
	if err != nil {
		return
	}
	for groupIndex := range groups {
		for profileIndex := range groups[groupIndex].Profiles {
			iccid := strings.TrimSpace(groups[groupIndex].Profiles[profileIndex].ICCID)
			groups[groupIndex].Profiles[profileIndex].Note = notes[iccid]
		}
	}
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
