package repository

import (
	"context"
	"strings"

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
	// LatestByRelatedCodes returns the most recently updated inspection task
	// for each provided 关联编号.
	LatestByRelatedCodes(context.Context, []string) (map[string]model.InspectionTask, error)
}

type inspectionTaskRepository struct {
	store *Store[model.InspectionTask]
}

func NewInspectionTaskRepository(db *gorm.DB) InspectionTaskRepository {
	return &inspectionTaskRepository{store: NewStore[model.InspectionTask](db)}
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

func (r *inspectionTaskRepository) LatestByRelatedCodes(ctx context.Context, relatedCodes []string) (map[string]model.InspectionTask, error) {
	latest := make(map[string]model.InspectionTask)
	codes := normalizeRelatedCodes(relatedCodes)
	if len(codes) == 0 {
		return latest, nil
	}
	var tasks []model.InspectionTask
	if err := r.store.ListByRelatedCodes(ctx, codes, &tasks); err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if existing, ok := latest[task.RelatedCode]; !ok || task.UpdatedAt.After(existing.UpdatedAt) {
			latest[task.RelatedCode] = task
		}
	}
	return latest, nil
}

// normalizeRelatedCodes uppercases, trims and deduplicates 关联编号 values.
func normalizeRelatedCodes(relatedCodes []string) []string {
	seen := make(map[string]bool, len(relatedCodes))
	codes := make([]string, 0, len(relatedCodes))
	for _, raw := range relatedCodes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	return codes
}
