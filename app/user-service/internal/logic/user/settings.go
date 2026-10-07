package user

import (
	"context"
	"encoding/json"
	"github.com/maomeng/aim/pkg/identity"

	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
	commonpb "github.com/maomeng/aim/pkg/pb/common"
	"gorm.io/gorm"
)

func (l *Logic) GetSettings(ctx context.Context, userID string) (*userpb.GetSettingsResp, error) {
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
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

func (l *Logic) UpdateSettings(ctx context.Context, userID string, req *userpb.UpdateSettingsReq) (*commonpb.BaseResponse, error) {
	if req.UserId != "" && req.UserId != userID {
		return nil, errors.New(errors.CodeForbidden, "account is not owned by caller")
	}
	if err := authorizeAccount(ctx, userID); err != nil {
		return nil, err
	}
	if req.ClearAiModelId && req.AiModelId != nil {
		return nil, errors.New(errors.CodeInvalidParam, "cannot set and clear ai_model_id")
	}
	if req.AiModelId != nil && identity.Validate(*req.AiModelId) != nil {
		return nil, errors.New(errors.CodeInvalidParam, "invalid ai_model_id")
	}
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
		s.AIModelID = req.AiModelId
	}
	if req.AiModelName != nil {
		s.AIModelName = *req.AiModelName
	}
	if req.SettingsJson != nil {
		s.SettingsJSON = *req.SettingsJson
	}
	if req.ClearAiModelId {
		s.AIModelID = nil
		s.AIModelName = ""
	}
	u.Settings = *s
	settingsJSON, err := json.Marshal(u.Settings)
	if err != nil {
		return nil, errors.Wrap(errors.CodeInternal, "encode settings failed", err)
	}
	if err := l.userRepo.Update(ctx, userID, map[string]any{
		"settings": string(settingsJSON),
	}); err != nil {
		return nil, errors.Wrap(errors.CodeDBError, "update settings failed", err)
	}
	return &commonpb.BaseResponse{Code: 0, Message: "ok"}, nil
}
