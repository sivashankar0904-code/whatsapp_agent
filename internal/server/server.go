// Package server builds the read-only message API.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/messages"
	"github.com/sivashankar/whatsapp_agent/internal/server/handlers"
	"github.com/sivashankar/whatsapp_agent/internal/server/middleware"
)

// New builds the Gin engine: CORS, a health check, and the read-only
// /messages endpoint. There is no auth — it binds to all interfaces because
// it is meant to be reached from other containers on the same docker
// network, not from the internet. Do not publish this port on a host
// interface without adding authentication first.
func New(msgStore *messages.Store) *gin.Engine {
	r := gin.Default()
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "whatsapp-agent"})
	})

	m := handlers.NewMessages(msgStore)
	r.GET("/messages", m.List)

	return r
}
