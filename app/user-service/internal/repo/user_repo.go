package repo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/maomeng/aim/app/user-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

var ErrInsufficientBalance = errors.New("insufficient balance")

type UserRepo struct {
	db *database.DB
}

func NewUserRepo(db *database.DB) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) Create(ctx context.Context, user *model.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("username = ?", username).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	var user model.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepo) Update(ctx context.Context, id int64, updates map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(updates).Error
}

func (r *UserRepo) BatchGetByIDs(ctx context.Context, ids []int64) ([]*model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var users []*model.User
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (r *UserRepo) Search(ctx context.Context, keyword string, page, pageSize int) ([]*model.User, int64, error) {
	var users []*model.User
	var total int64

	like := fmt.Sprintf("%%%s%%", keyword)
	db := r.db.WithContext(ctx).Model(&model.User{}).Where("username LIKE ?", like)

	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * pageSize

	err := db.Order("id ASC").Offset(offset).Limit(pageSize).Find(&users).Error
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *UserRepo) ExistsByUsername(ctx context.Context, username string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("username = ?", username).Count(&count).Error
	return count > 0, err
}

func (r *UserRepo) ExistsByPhone(ctx context.Context, phone string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("phone = ?", phone).Count(&count).Error
	return count > 0, err
}

func (r *UserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Where("email = ?", email).Count(&count).Error
	return count > 0, err
}

func (r *UserRepo) ListAllIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.User{}).Pluck("id", &ids).Error
	return ids, err
}

func (r *UserRepo) UpdateBalance(ctx context.Context, userID int64, delta float64) (float64, error) {
	result := r.db.WithContext(ctx).Exec(
		"UPDATE users SET balance = balance + ? WHERE id = ? AND balance + ? >= 0",
		delta, userID, delta,
	)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, ErrInsufficientBalance
	}
	return r.GetBalance(ctx, userID)
}

func (r *UserRepo) GetBalance(ctx context.Context, userID int64) (float64, error) {
	var user model.User
	if err := r.db.WithContext(ctx).Select("balance").First(&user, userID).Error; err != nil {
		return 0, err
	}
	return user.Balance, nil
}

func IsNotFound(err error) bool {
	return err != nil && err.Error() == gorm.ErrRecordNotFound.Error()
}
