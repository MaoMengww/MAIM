package middleware

import (
	"context"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/pkg/jwt"
	"google.golang.org/grpc/metadata"
)

const (
	CtxKeyUserID    = "user_id"
	CtxKeyDeviceID  = "device_id"
	CtxKeyRequestID = "request_id"
)

var authWhitelist = map[string]bool{
	"/api/v1/auth/register":        true,
	"/api/v1/auth/login":           true,
	"/api/v1/auth/refresh":         true,
	"/api/v1/auth/oauth/:provider": true,
	"/health":                      true,
	"/metrics":                     true,
}

func AuthRequired(jwtMgr *jwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authWhitelist[c.FullPath()] {
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(401, gin.H{
				"code":    401,
				"message": "missing authorization header",
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(401, gin.H{
				"code":    401,
				"message": "invalid authorization format",
			})
			return
		}

		claims, err := jwtMgr.Parse(parts[1])
		if err != nil || claims == nil {
			c.AbortWithStatusJSON(401, gin.H{
				"code":    401,
				"message": "invalid or expired token",
			})
			return
		}

		userID, err := strconv.ParseInt(claims.UserID, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{
				"code":    401,
				"message": "invalid user_id in token",
			})
			return
		}

		c.Set(CtxKeyUserID, userID)
		c.Set(CtxKeyDeviceID, claims.Subject)

		c.Next()
	}
}

func WithGRPCMetadata(c *gin.Context) context.Context {
	ctx := context.Background()

	userID, exists := c.Get(CtxKeyUserID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, "user-id", strconv.FormatInt(userID.(int64), 10))
	}

	deviceID, exists := c.Get(CtxKeyDeviceID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, "device-id", deviceID.(string))
	}

	requestID, exists := c.Get(CtxKeyRequestID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, "request-id", requestID.(string))
	}

	return ctx
}
