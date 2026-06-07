package push

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/maomeng/aim/app/ws-gateway/internal/presence"
	"github.com/maomeng/aim/app/ws-gateway/internal/session"
	"github.com/maomeng/aim/pkg/consts"
)

type PresencePusher struct {
	router   *Router
	sessions *session.Manager
}

func NewPresencePusher(router *Router, sessions *session.Manager) *PresencePusher {
	return &PresencePusher{router: router, sessions: sessions}
}

func (p *PresencePusher) PushPresence(ctx context.Context, event presence.PresenceEvent) error {
	payload, _ := json.Marshal(map[string]any{
		"type":    consts.EventPresence,
		"user_id": strconv.FormatInt(event.UserID, 10),
		"status":  string(event.Status),
	})
	users := p.sessions.GetAllUsers()
	for _, uid := range users {
		_ = p.router.PushToUser(ctx, uid, payload)
	}
	return nil
}
