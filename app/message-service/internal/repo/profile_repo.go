package repo

import (
	"context"

	"github.com/maomeng/aim/pkg/database"
	"gorm.io/gorm"
)

// ProfileRepo reads the account and relationship data the message hot path needs
// for display names and block checks. Those tables belong to the user domain and
// are read-only here (ADR-0007): the message domain never writes them, it only
// avoids depending on user-service availability while sending a message.
type ProfileRepo struct {
	DB *database.DB
}

func NewProfileRepo(db *database.DB) *ProfileRepo {
	return &ProfileRepo{DB: db}
}

// Profile is the display projection of one account.
type Profile struct {
	ID       string
	Username string
	Avatar   string
}

// UserProfile returns the display projection of one account. A missing account
// yields an error and a zero value.
func (r *ProfileRepo) UserProfile(ctx context.Context, userID string) (Profile, error) {
	var profile Profile
	if userID == "" {
		return profile, gorm.ErrRecordNotFound
	}
	err := r.DB.WithContext(ctx).Table(`"user".users`).
		Select("id, username, avatar").Where("id = ?", userID).Take(&profile).Error
	return profile, err
}

// UserProfiles returns the display projection of the requested accounts. Unknown
// ids are absent from the result.
func (r *ProfileRepo) UserProfiles(ctx context.Context, userIDs []string) (map[string]Profile, error) {
	profiles := make(map[string]Profile, len(userIDs))
	if len(userIDs) == 0 {
		return profiles, nil
	}
	var rows []Profile
	if err := r.DB.WithContext(ctx).Table(`"user".users`).
		Select("id, username, avatar").Where("id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		profiles[row.ID] = row
	}
	return profiles, nil
}

// UserNames returns id → username for the requested accounts. Unknown ids are
// absent from the result.
func (r *ProfileRepo) UserNames(ctx context.Context, userIDs []string) (map[string]string, error) {
	names := make(map[string]string, len(userIDs))
	if len(userIDs) == 0 {
		return names, nil
	}
	var rows []struct {
		ID       string `gorm:"column:id"`
		Username string `gorm:"column:username"`
	}
	if err := r.DB.WithContext(ctx).Table(`"user".users`).
		Select("id, username").Where("id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		names[row.ID] = row.Username
	}
	return names, nil
}

// AllUserIDs lists every account id, used by broadcast sends.
func (r *ProfileRepo) AllUserIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.DB.WithContext(ctx).Table(`"user".users`).Pluck("id", &ids).Error
	return ids, err
}
