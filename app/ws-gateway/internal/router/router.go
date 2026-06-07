package router

import (
	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/ws-gateway/internal/config"
	"github.com/maomeng/aim/app/ws-gateway/internal/handler"
)

func Register(r *gin.Engine, wsHandler *handler.WSHandler, cfg *config.Config) {
	r.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/ws", wsHandler.Upgrade)
	r.GET("/ws/bot", wsHandler.UpgradeBot)
}
