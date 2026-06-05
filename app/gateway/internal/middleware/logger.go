package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/pkg/logx"
)

func Logger(logger logx.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		duration := time.Since(start)
		statusCode := c.Writer.Status()

		fields := []logx.LogField{
			logx.String("method", method),
			logx.String("path", path),
			logx.Int("status", statusCode),
			logx.String("duration", duration.String()),
		}

		if requestID, exists := c.Get(CtxKeyRequestID); exists {
			fields = append(fields, logx.String("request_id", requestID.(string)))
		}
		if userID, exists := c.Get(CtxKeyUserID); exists {
			fields = append(fields, logx.Int("user_id", int(userID.(int64))))
		}

		logger.WithFields(fields...).WithDuration(duration).Info("http request")
	}
}
