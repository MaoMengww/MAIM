package user

import (
	"context"
	"fmt"
	"strconv"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/zeromicro/go-zero/core/stores/redis"
)

type StatusLogic struct {
	rdb *redis.Redis
}

func NewStatusLogic(rdb *redis.Redis) *StatusLogic {
	return &StatusLogic{rdb: rdb}
}

func (l *StatusLogic) BatchGetStatus(ctx context.Context, req *userpb.BatchGetStatusReq) (*userpb.BatchGetStatusResp, error) {
	if len(req.UserIds) == 0 {
		return &userpb.BatchGetStatusResp{}, nil
	}
	statuses := make([]*userpb.UserStatus, 0, len(req.UserIds))
	for _, uid := range req.UserIds {
		key := fmt.Sprintf("user:%d:devices", uid)
		result, err := l.rdb.HgetallCtx(ctx, key)
		if err != nil {
			continue
		}
		isOnline := len(result) > 0
		devices := make([]*userpb.DeviceInfo, 0, len(result))
		for deviceID, tsStr := range result {
			ts, _ := strconv.ParseInt(tsStr, 10, 64)
			devices = append(devices, &userpb.DeviceInfo{
				DeviceId:     deviceID,
				LastActiveAt: ts,
			})
		}
		statuses = append(statuses, &userpb.UserStatus{
			UserId:   uid,
			IsOnline: isOnline,
			Devices:  devices,
		})
	}
	return &userpb.BatchGetStatusResp{Statuses: statuses}, nil
}
