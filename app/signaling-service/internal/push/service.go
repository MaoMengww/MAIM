package push

import (
	"context"
	"fmt"
	"strconv"

	"github.com/maomeng/aim/app/signaling-service/internal/repo"
	"github.com/maomeng/aim/pkg/logx"
)

type Service struct {
	fcm    *FCMSender
	apns   *APNSSender
	repo   *repo.DeviceTokenRepo
	logger logx.Logger
}

func NewService(fcm *FCMSender, apns *APNSSender, repo *repo.DeviceTokenRepo, logger logx.Logger) *Service {
	return &Service{fcm: fcm, apns: apns, repo: repo, logger: logger}
}

func (s *Service) PushToOfflineUsers(ctx context.Context, userIDs []int64, title, body string, data map[string]string) {
	tokens, err := s.repo.GetByUserIDs(ctx, userIDs)
	if err != nil {
		return
	}
	for _, dt := range tokens {
		switch dt.Provider {
		case "fcm":
			if s.fcm != nil {
				if err := s.fcm.Send(ctx, dt.Token, title, body, data); err != nil {
					s.logger.WithContext(ctx).Errorf("fcm push: user=%d err=%v", dt.UserID, err)
				}
			}
		case "apns":
			if s.apns != nil {
				if err := s.apns.Send(ctx, dt.Token, "com.aim.chat", title, body, data); err != nil {
					s.logger.WithContext(ctx).Errorf("apns push: user=%d err=%v", dt.UserID, err)
				}
			}
		}
	}
}

func (s *Service) RegisterDevice(ctx context.Context, userID int64, deviceID, platform, token, provider string) error {
	return s.repo.Upsert(ctx, userID, deviceID, platform, token, provider)
}

func (s *Service) UnregisterDevice(ctx context.Context, userID int64, deviceID string) error {
	return s.repo.Delete(ctx, userID, deviceID)
}

func BuildPushData(convID int64, senderName, preview string) map[string]string {
	return map[string]string{
		"conv_id":     strconv.FormatInt(convID, 10),
		"sender_name": senderName,
		"preview":     preview,
		"click_action": "OPEN_CONV",
	}
}

func BuildTitle(senderName string) string {
	if senderName == "" {
		return "新消息"
	}
	return fmt.Sprintf("%s 发来消息", senderName)
}
