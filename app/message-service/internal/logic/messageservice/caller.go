package messageservicelogic

import (
	"context"
	"github.com/maomeng/aim/pkg/identity"

	"google.golang.org/grpc/metadata"
)

// callerUserID returns the authenticated UUID from incoming metadata.
func callerUserID(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("user-id")
	if len(values) != 1 || identity.Validate(values[0]) != nil {
		return ""
	}
	return values[0]
}

func validateIdentities(ids ...string) error {
	for _, id := range ids {
		if err := identity.Validate(id); err != nil {
			return err
		}
	}
	return nil
}

func requireCaller(ctx context.Context, userID string) error {
	caller := callerUserID(ctx)
	if caller == "" {
		return ErrUserIDMissing
	}
	if caller != userID {
		return ErrSendAsOtherUser
	}
	return nil
}
