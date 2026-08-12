package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/db"
)

func (s *Server) handleDeviceVoWiFiHistory(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	history, err := db.GetVoWiFiHistory(deviceIDParam(c), limit, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, history)
}
