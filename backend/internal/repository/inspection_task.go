package repository

import (
	"context"

	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/dto"
	"github.com/blueship581/aircraft-component-airworthiness-release/backend/internal/model"
	"gorm.io/gorm"
)

// InspectionTaskRepository owns all persistence operations for 检查任务.
type InspectionTaskRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.InspectionTask], error)
	Get(context.Context, uint) (model.InspectionTask, error)
	Create(context.Context, *model.InspectionTask) error
	Update(context.Context, uint, uint, *model.InspectionTask) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	LatestPassedByRelatedCodes(context.Context, []string) (map[string]model.InspectionTask, error)
	ListByCodes(context.Context, []string) (map[string]model.InspectionTask, error)
}

type inspectionTaskRepository struct {
	store *Store[model.InspectionTask]
	db    *gorm.DB
}

func NewInspectionTaskRepository(db *gorm.DB) InspectionTaskRepository {
	return &inspectionTaskRepository{store: NewStore[model.InspectionTask](db), db: db}
}

func (r *inspectionTaskRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.InspectionTask], error) {
	return r.store.List(ctx, q)
}
func (r *inspectionTaskRepository) Get(ctx context.Context, id uint) (model.InspectionTask, error) {
	return r.store.Get(ctx, id)
}
func (r *inspectionTaskRepository) Create(ctx context.Context, item *model.InspectionTask) error {
	return r.store.Create(ctx, item)
}
func (r *inspectionTaskRepository) Update(ctx context.Context, id, version uint, item *model.InspectionTask) error {
	return r.store.Update(ctx, id, version, item)
}
func (r *inspectionTaskRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *inspectionTaskRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

// LatestPassedByRelatedCodes returns the most recently updated passed inspection
// task for each related code. Rows are ordered newest first, so the first row per
// related code is the one currently in force.
func (r *inspectionTaskRepository) LatestPassedByRelatedCodes(ctx context.Context, codes []string) (map[string]model.InspectionTask, error) {
	result := make(map[string]model.InspectionTask)
	if len(codes) == 0 {
		return result, nil
	}
	var items []model.InspectionTask
	if err := r.db.WithContext(ctx).Where("related_code IN ? AND status = ?", codes, "passed").
		Order("updated_at DESC, id DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, exists := result[item.RelatedCode]; !exists {
			result[item.RelatedCode] = item
		}
	}
	return result, nil
}

// ListByCodes returns the current inspection tasks indexed by their business code.
func (r *inspectionTaskRepository) ListByCodes(ctx context.Context, codes []string) (map[string]model.InspectionTask, error) {
	result := make(map[string]model.InspectionTask)
	if len(codes) == 0 {
		return result, nil
	}
	var items []model.InspectionTask
	if err := r.db.WithContext(ctx).Where("code IN ?", codes).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		result[item.Code] = item
	}
	return result, nil
}
