package offline

import (
	"context"

	"github.com/maomeng/aim/app/realtime-service/internal/repo"
	"github.com/maomeng/aim/pkg/logx"
)

type Service struct {
	fcm    *FCMSender
	apns   *APNSSender
	repo   *repo.DeviceTokenRepo
	logger logx.Logger
	topic  string
}

func NewService(fcm *FCMSender, apns *APNSSender, repo *repo.DeviceTokenRepo, logger logx.Logger, topic string) *Service {
	return &Service{fcm: fcm, apns: apns, repo: repo, logger: logger, topic: topic}
}

func (s *Service) PushToOfflineUsers(ctx context.Context, userIDs []int64, title, body string, data map[string]string) {
	tokens, err := s.repo.GetByUserIDs(ctx, userIDs)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("offline device token lookup: %v", err)
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
				if err := s.apns.Send(ctx, dt.Token, s.topic, title, body, data); err != nil {
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
