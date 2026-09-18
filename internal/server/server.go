// Package server builds the read-only message API.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/chats"
	"github.com/sivashankar/whatsapp_agent/internal/messages"
	"github.com/sivashankar/whatsapp_agent/internal/registrations"
	"github.com/sivashankar/whatsapp_agent/internal/scheduler"
	"github.com/sivashankar/whatsapp_agent/internal/server/handlers"
	"github.com/sivashankar/whatsapp_agent/internal/server/middleware"
)

// New builds the Gin engine: CORS, a health check, the read-only /messages
// endpoint, CRUD over /chats and /registrations, and an on-demand trigger
// for the scheduled job. There is no auth — it binds to all interfaces
// because it is meant to be reached from other containers on the same
// docker network, not from the internet. Do not publish this port on a host
// interface without adding authentication first.
func New(msgStore *messages.Store, chatStore *chats.Store, regStore *registrations.Store, sched *scheduler.Scheduler) *gin.Engine {
	r := gin.Default()
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "whatsapp-agent"})
	})

	m := handlers.NewMessages(msgStore)
	r.GET("/messages", m.List)

	ch := handlers.NewChats(chatStore)
	r.GET("/chats", ch.List)
	r.GET("/chats/by-name/:name", ch.GetByName)
	r.GET("/chats/:jid", ch.Get)
	r.PUT("/chats/:jid", ch.Upsert)
	r.DELETE("/chats/:jid", ch.Delete)

	reg := handlers.NewRegistrations(regStore)
	r.GET("/registrations", reg.List)
	r.POST("/registrations", reg.Register)
	r.DELETE("/registrations/:jid", reg.Unregister)

	sc := handlers.NewScheduler(sched)
	r.POST("/scheduler/run", sc.Run)

	return r
}
