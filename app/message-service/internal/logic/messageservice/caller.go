package messageservicelogic

import (
	"context"
	"strconv"

	"google.golang.org/grpc/metadata"
)

// callerUserID returns the authenticated caller carried in gRPC metadata by the
// gateway's interceptor, or 0 when it is absent or malformed.
func callerUserID(ctx context.Context) int64 {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	values := md.Get("user-id")
	if len(values) == 0 {
		return 0
	}
	id, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
