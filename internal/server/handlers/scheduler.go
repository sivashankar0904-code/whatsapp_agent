package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sivashankar/whatsapp_agent/internal/scheduler"
)

// Scheduler handles the /scheduler endpoints, backed by a Scheduler.
type Scheduler struct {
	sched *scheduler.Scheduler
}

// NewScheduler returns a Scheduler handler bound to sched.
func NewScheduler(sched *scheduler.Scheduler) *Scheduler {
	return &Scheduler{sched: sched}
}

// Run serves POST /scheduler/run, triggering the scheduled job immediately
// instead of waiting for the next tick. Returns 409 if a run (ticked or
// on-demand) is already in flight.
func (h *Scheduler) Run(c *gin.Context) {
	if started := h.sched.RunOnce(c.Request.Context()); !started {
		c.JSON(http.StatusConflict, gin.H{"error": "a scheduler run is already in progress"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ran"})
}
