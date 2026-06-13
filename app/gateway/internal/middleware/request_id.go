package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/maomeng/aim/pkg/consts"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(consts.HeaderRequestID)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		c.Set(CtxKeyRequestID, requestID)
		c.Header(consts.HeaderRequestID, requestID)
		c.Next()
	}
}
