package user

import (
	"context"
	"testing"

	"github.com/maomeng/aim/app/user-service/internal/model"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/gorm"
)

func TestGetSettings_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{
		ID: 1,
		Settings: model.UserSettings{
			Language:            "zh-CN",
			NotificationEnabled: true,
			SoundEnabled:        true,
			VibrationEnabled:    true,
			Theme:               "dark",
			SettingsJSON:        `{"custom":"value"}`,
		},
	}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)

	l := newTestLogic(mockUser)
	resp, err := l.GetSettings(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "zh-CN", resp.Language)
	assert.True(t, resp.NotificationEnabled)
	assert.True(t, resp.SoundEnabled)
	assert.True(t, resp.VibrationEnabled)
	assert.Equal(t, "dark", resp.Theme)
	assert.Equal(t, `{"custom":"value"}`, resp.SettingsJson)
}

func TestGetSettings_DefaultValues(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Settings: model.UserSettings{}}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)

	l := newTestLogic(mockUser)
	resp, err := l.GetSettings(context.Background(), 1)
	assert.NoError(t, err)
	assert.Equal(t, "", resp.Language)
	assert.False(t, resp.NotificationEnabled)
}

func TestGetSettings_NotFound(t *testing.T) {
	mockUser := new(MockUserRepo)
	mockUser.On("GetByID", mock.Anything, int64(999)).Return(nil, gorm.ErrRecordNotFound)

	l := newTestLogic(mockUser)
	_, err := l.GetSettings(context.Background(), 999)
	assert.Error(t, err)
}

func TestUpdateSettings_Success(t *testing.T) {
	mockUser := new(MockUserRepo)
	u := &model.User{ID: 1, Settings: model.UserSettings{}}
	mockUser.On("GetByID", mock.Anything, int64(1)).Return(u, nil)
	mockUser.On("Update", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	lang := "en-US"
	notif := true
	theme := "light"

	l := newTestLogic(mockUser)
	resp, err := l.UpdateSettings(context.Background(), 1, &userpb.UpdateSettingsReq{
		Language:            &lang,
		NotificationEnabled: &notif,
		Theme:               &theme,
	})
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Message)
}
