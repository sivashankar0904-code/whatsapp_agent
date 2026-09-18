package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/chats"
)

// Chats handles the /chats endpoints, backed by a chat store.
type Chats struct {
	store *chats.Store
}

// NewChats returns a Chats handler bound to store.
func NewChats(store *chats.Store) *Chats {
	return &Chats{store: store}
}

type upsertChatRequest struct {
	DisplayName string `json:"display_name"`
	IsGroup     bool   `json:"is_group"`
}

// Upsert serves PUT /chats/:jid, creating or updating the chat with :jid.
func (h *Chats) Upsert(c *gin.Context) {
	var req upsertChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	chat, err := h.store.Upsert(c.Request.Context(), c.Param("jid"), req.DisplayName, req.IsGroup)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, chat)
}

// Get serves GET /chats/:jid.
func (h *Chats) Get(c *gin.Context) {
	chat, err := h.store.Get(c.Request.Context(), c.Param("jid"))
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, chat)
}

// GetByName serves GET /chats/by-name/:name — the chat_jid lookup for a
// known group (or 1:1 chat) display name.
func (h *Chats) GetByName(c *gin.Context) {
	chatJID, err := h.store.GetIDByName(c.Request.Context(), c.Param("name"))
	if err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"chat_jid": chatJID})
}

// List serves GET /chats.
func (h *Chats) List(c *gin.Context) {
	out, err := h.store.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

// Delete serves DELETE /chats/:jid.
func (h *Chats) Delete(c *gin.Context) {
	if err := h.store.Delete(c.Request.Context(), c.Param("jid")); err != nil {
		writeChatError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "chat_jid": c.Param("jid")})
}

func writeChatError(c *gin.Context, err error) {
	if errors.Is(err, chats.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}
