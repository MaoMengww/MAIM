package user

import (
	"context"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"gorm.io/gorm"
)

func (l *Logic) GetSettings(ctx context.Context, userID int64) (*userpb.GetSettingsResp, error) {
	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "user not found")
		}
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	s := u.Settings
	return &userpb.GetSettingsResp{
		Language:            s.Language,
		NotificationEnabled: s.NotificationEnabled,
		SoundEnabled:        s.SoundEnabled,
		VibrationEnabled:    s.VibrationEnabled,
		Theme:               s.Theme,
		SettingsJson:        s.SettingsJSON,
		AiModelId:           s.AIModelID,
		AiModelName:         s.AIModelName,
	}, nil
}

func (l *Logic) UpdateSettings(ctx context.Context, userID int64, req *userpb.UpdateSettingsReq) (*commonpb.BaseResponse, error) {
	u, err := l.userRepo.GetByID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New(errors.CodeNotFound, "user not found")
		}
		return nil, errors.Wrap(errors.CodeDBError, "get user failed", err)
	}
	s := &u.Settings
	if req.Language != nil {
		s.Language = *req.Language
	}
	if req.NotificationEnabled != nil {
		s.NotificationEnabled = *req.NotificationEnabled
	}
	if req.SoundEnabled != nil {
		s.SoundEnabled = *req.SoundEnabled
	}
	if req.VibrationEnabled != nil {
		s.VibrationEnabled = *req.VibrationEnabled
	}
	if req.Theme != nil {
		s.Theme = *req.Theme
	}
	if req.AiModelId != nil {
		s.AIModelID = *req.AiModelId
	}
	if req.AiModelName != nil {
		s.AIModelName = *req.AiModelName
	}
	if req.SettingsJson != nil {
		s.SettingsJSON = *req.SettingsJson
	}
	u.Settings = *s
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"settings": u.Settings,
	}); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update settings failed", err)
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}
