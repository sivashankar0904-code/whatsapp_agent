package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/messages"
)

// Messages handles the /messages endpoint, backed by a message store.
type Messages struct {
	store *messages.Store
}

// NewMessages returns a Messages handler bound to store.
func NewMessages(store *messages.Store) *Messages {
	return &Messages{store: store}
}

// List serves GET /messages.
//
// Query parameters:
//
//	chat   filter to one chat JID (e.g. 9199...@s.whatsapp.net or ...@g.us)
//	since  return only rows with id > since, for polling without duplicates
//	limit  max rows to return, default 50, capped at 500
//
// Rows come back oldest-first, matching a chat's natural reading order.
func (h *Messages) List(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": `"limit" must be a positive integer`})
			return
		}
		limit = min(n, 500)
	}

	since := int64(0)
	if v := c.Query("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": `"since" must be an integer message id`})
			return
		}
		since = n
	}

	out, err := h.store.List(c.Request.Context(), c.Query("chat"), since, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
