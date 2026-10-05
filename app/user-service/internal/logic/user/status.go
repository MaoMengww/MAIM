package user

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/connections"
)

type StatusLogic struct {
	registry *connections.Store
}

func NewStatusLogic(registry *connections.Store) *StatusLogic {
	return &StatusLogic{registry: registry}
}

func (l *StatusLogic) BatchGetStatus(ctx context.Context, req *userpb.BatchGetStatusReq) (*userpb.BatchGetStatusResp, error) {
	if len(req.UserIds) == 0 {
		return &userpb.BatchGetStatusResp{}, nil
	}
	statuses := make([]*userpb.UserStatus, 0, len(req.UserIds))
	for _, uid := range req.UserIds {
		routes, err := l.registry.List(ctx, connections.User, uid)
		if err != nil {
			return nil, err
		}
		devices := make([]*userpb.DeviceInfo, 0, len(routes))
		for _, route := range routes {
			devices = append(devices, &userpb.DeviceInfo{
				DeviceId:     route.DeviceID,
				LastActiveAt: route.LastActive,
			})
		}
		statuses = append(statuses, &userpb.UserStatus{
			UserId:   uid,
			IsOnline: len(routes) > 0,
			Devices:  devices,
		})
	}
	return &userpb.BatchGetStatusResp{Statuses: statuses}, nil
}
