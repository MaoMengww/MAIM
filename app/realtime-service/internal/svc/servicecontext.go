package svc

import (
	"github.com/maomeng/aim/app/realtime-service/internal/offline"
	"github.com/maomeng/aim/app/realtime-service/internal/repo"
	"github.com/maomeng/aim/app/realtime-service/internal/transport"
	registry "github.com/maomeng/aim/pkg/connections"
)

type ServiceContext struct {
	NotifRepo       *repo.NotificationRepo
	PresenceChecker *registry.Store
	PushService     *offline.Service
	Router          *transport.Router
}
