package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/realtime-service/internal/handler"
)

func Register(r *gin.Engine, h *handler.WSHandler) {
	health := func(c *gin.Context) {
		if !h.Router.Ready() {
			c.String(http.StatusServiceUnavailable, "not ready")
			return
		}
		c.String(http.StatusOK, "ok")
	}
	r.GET("/health", health)
	r.GET("/readiness", health)
	r.GET("/ws", h.Upgrade)
	r.GET("/ws/bot", h.UpgradeBot)
}
