package middleware

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/maomeng/aim/app/gateway/internal/response"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/consts"
	"github.com/maomeng/aim/pkg/identity"
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

func AuthRequired(jwtMgr *jwt.Manager, users userpb.UserServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authWhitelist[c.FullPath()] {
			c.Next()
			return
		}

		authHeader := c.GetHeader(consts.HeaderToken)
		if authHeader == "" {
			response.Unauthorized(c, "missing authorization header")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Unauthorized(c, "invalid authorization format")
			c.Abort()
			return
		}

		claims, err := jwtMgr.Parse(parts[1])
		if err != nil || claims == nil {
			response.Unauthorized(c, "invalid or expired token")
			c.Abort()
			return
		}

		if err := identity.Validate(claims.UserID); err != nil {
			response.Unauthorized(c, "invalid user_id in token")
			c.Abort()
			return
		}
		validated, err := users.ValidateToken(c.Request.Context(), &userpb.ValidateTokenReq{AccessToken: parts[1]})
		if err != nil || !validated.GetValid() || validated.GetUserId() != claims.UserID || validated.GetDeviceId() != claims.DeviceID {
			response.Unauthorized(c, "invalid or revoked session")
			c.Abort()
			return
		}

		c.Set(CtxKeyUserID, claims.UserID)
		c.Set(CtxKeyDeviceID, claims.DeviceID)

		c.Next()
	}
}

func WithGRPCMetadata(c *gin.Context) context.Context {
	ctx := c.Request.Context()

	userID, exists := c.Get(CtxKeyUserID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, consts.MetadataKeyUserID, userID.(string))
	}

	deviceID, exists := c.Get(CtxKeyDeviceID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, consts.MetadataKeyDeviceID, deviceID.(string))
	}

	requestID, exists := c.Get(CtxKeyRequestID)
	if exists {
		ctx = metadata.AppendToOutgoingContext(ctx, consts.MetadataKeyRequestID, requestID.(string))
	}

	return ctx
}
