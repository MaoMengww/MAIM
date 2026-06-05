package svc

import "context"

const CtxKeyUserID = "user_id"

func UserIDFromCtx(ctx context.Context) int64 {
	if v, ok := ctx.Value(CtxKeyUserID).(int64); ok {
		return v
	}
	return 0
}
