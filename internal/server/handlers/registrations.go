package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/registrations"
)

// Registrations handles the /registrations endpoints, backed by a
// registration store.
type Registrations struct {
	store *registrations.Store
}

// NewRegistrations returns a Registrations handler bound to store.
func NewRegistrations(store *registrations.Store) *Registrations {
	return &Registrations{store: store}
}

type registerRequest struct {
	ChatJID string `json:"chat_jid" binding:"required"`
}

// Register serves POST /registrations, adding a chat_jid to the scheduled
// job's chat list.
func (h *Registrations) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chat_jid is required"})
		return
	}

	reg, err := h.store.Register(c.Request.Context(), req.ChatJID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, reg)
}

// List serves GET /registrations.
func (h *Registrations) List(c *gin.Context) {
	out, err := h.store.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// Unregister serves DELETE /registrations/:jid.
func (h *Registrations) Unregister(c *gin.Context) {
	if err := h.store.Unregister(c.Request.Context(), c.Param("jid")); err != nil {
		if errors.Is(err, registrations.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "registration not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unregistered", "chat_jid": c.Param("jid")})
}
