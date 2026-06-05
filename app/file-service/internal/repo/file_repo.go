package repo

import (
	"context"

	"github.com/maomeng/aim/app/file-service/internal/model"
	"github.com/maomeng/aim/pkg/database"
)

type FileRepo struct {
	db *database.DB
}

func NewFileRepo(db *database.DB) *FileRepo {
	return &FileRepo{db: db}
}

func (r *FileRepo) Create(ctx context.Context, f *model.File) error {
	return r.db.WithContext(ctx).Create(f).Error
}

func (r *FileRepo) GetByID(ctx context.Context, id int64) (*model.File, error) {
	var f model.File
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&f).Error
	return &f, err
}

func (r *FileRepo) BatchGetByIDs(ctx context.Context, ids []int64) ([]model.File, error) {
	var files []model.File
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&files).Error
	return files, err
}

func (r *FileRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.File{}).Error
}

func (r *FileRepo) BatchDelete(ctx context.Context, ids []int64) error {
	return r.db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.File{}).Error
}

func (r *FileRepo) Update(ctx context.Context, f *model.File) error {
	return r.db.WithContext(ctx).Save(f).Error
}

type FileRepoInterface interface {
	Create(ctx context.Context, f *model.File) error
	GetByID(ctx context.Context, id int64) (*model.File, error)
	BatchGetByIDs(ctx context.Context, ids []int64) ([]model.File, error)
	Delete(ctx context.Context, id int64) error
	BatchDelete(ctx context.Context, ids []int64) error
	Update(ctx context.Context, f *model.File) error
}

var _ FileRepoInterface = (*FileRepo)(nil)
