package push

import (
	"context"
	"encoding/json"

	"github.com/gorilla/websocket"
	"github.com/maomeng/aim/app/ws-gateway/internal/session"
	"github.com/maomeng/aim/pkg/logx"
	pushpb "github.com/maomeng/aim/pkg/pb/push"
)

type Router struct {
	sessions *session.Manager
	botSess  *session.Manager
	logger   logx.Logger
}

func NewRouter(sm *session.Manager) *Router {
	return &Router{sessions: sm, botSess: session.NewManager()}
}

func (r *Router) BotSess() *session.Manager { return r.botSess }

func (r *Router) PushToUser(ctx context.Context, userID int64, message json.RawMessage) error {
	sessions := r.sessions.GetByUserID(userID)
	for _, s := range sessions {
		_ = s.WriteMessage(message)
	}
	return nil
}

func (r *Router) PushToBot(ctx context.Context, botID int64, message json.RawMessage) error {
	sessions := r.botSess.GetByUserID(botID)
	for _, s := range sessions {
		_ = s.WriteMessage(message)
	}
	return nil
}

func (r *Router) PushToConvExcept(ctx context.Context, excludeUserID int64, message json.RawMessage) error {
	users := r.sessions.GetAllUsers()
	for _, uid := range users {
		if uid == excludeUserID {
			continue
		}
		r.PushToUser(ctx, uid, message)
	}
	return nil
}

// Implements the gRPC InternalPushService interface
type PushServer struct {
	router *Router
	pushpb.UnimplementedInternalPushServiceServer
}

func NewPushServer(router *Router) *PushServer {
	return &PushServer{router: router}
}

func (s *PushServer) PushToUsers(ctx context.Context, req *pushpb.PushToUsersReq) (*pushpb.PushToUsersResp, error) {
	for _, uid := range req.UserIds {
		_ = s.router.PushToUser(ctx, uid, req.Message)
	}
	return &pushpb.PushToUsersResp{}, nil
}

func (s *PushServer) PushToBot(ctx context.Context, req *pushpb.PushToBotReq) (*pushpb.PushToBotResp, error) {
	_ = s.router.PushToBot(ctx, req.BotId, req.Message)
	return &pushpb.PushToBotResp{}, nil
}

func (s *PushServer) PushToConv(ctx context.Context, req *pushpb.PushToConvReq) (*pushpb.PushToConvResp, error) {
	// Broadcast to all online users; frontend filters by conv_id from the message payload
	_ = s.router.PushToConvExcept(ctx, 0, req.Message)
	return &pushpb.PushToConvResp{}, nil
}

func (s *PushServer) PushToUser(ctx context.Context, req *pushpb.PushToUserReq) (*pushpb.PushToUserResp, error) {
	_ = s.router.PushToUser(ctx, req.UserId, req.Message)
	return &pushpb.PushToUserResp{}, nil
}

// Keep imports alive
var _ = websocket.TextMessage
