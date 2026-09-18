package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS is permissive: this API is read-only and reached only from other
// containers on the same docker network, not the internet — see the
// warning on server.New.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
