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
	ID       int64
	Username string
	Avatar   string
}

// UserProfile returns the display projection of one account. A missing account
// yields an error and a zero value.
func (r *ProfileRepo) UserProfile(ctx context.Context, userID int64) (Profile, error) {
	var profile Profile
	if userID == 0 {
		return profile, gorm.ErrRecordNotFound
	}
	err := r.DB.WithContext(ctx).Table(`"user".users`).
		Select("id, username, avatar").Where("id = ?", userID).Take(&profile).Error
	return profile, err
}

// UserProfiles returns the display projection of the requested accounts. Unknown
// ids are absent from the result.
func (r *ProfileRepo) UserProfiles(ctx context.Context, userIDs []int64) (map[int64]Profile, error) {
	profiles := make(map[int64]Profile, len(userIDs))
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
func (r *ProfileRepo) UserNames(ctx context.Context, userIDs []int64) (map[int64]string, error) {
	names := make(map[int64]string, len(userIDs))
	if len(userIDs) == 0 {
		return names, nil
	}
	var rows []struct {
		ID       int64  `gorm:"column:id"`
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
func (r *ProfileRepo) AllUserIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.DB.WithContext(ctx).Table(`"user".users`).Pluck("id", &ids).Error
	return ids, err
}

// IsBlocked reports whether either side has blocked the other, matching the
// relationship domain's definition of a blocked pair.
func (r *ProfileRepo) IsBlocked(ctx context.Context, userID, targetID int64) (bool, error) {
	if userID == 0 || targetID == 0 {
		return false, nil
	}
	var count int64
	err := r.DB.WithContext(ctx).Table(`"user".user_blocks`).
		Where("(user_id = ? AND blocked_user_id = ?) OR (user_id = ? AND blocked_user_id = ?)",
			userID, targetID, targetID, userID).
		Count(&count).Error
	return count > 0, err
}

// IsBlockedAny reports whether any of the targets forms a blocked pair with the
// user.
func (r *ProfileRepo) IsBlockedAny(ctx context.Context, userID int64, targetIDs []int64) (bool, error) {
	for _, targetID := range targetIDs {
		blocked, err := r.IsBlocked(ctx, userID, targetID)
		if err != nil {
			return false, err
		}
		if blocked {
			return true, nil
		}
	}
	return false, nil
}
