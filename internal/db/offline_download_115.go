package db

import (
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrOfflineDownload115TaskDeleted = errors.New("115 Open offline download task has been deleted")

func validateOfflineDownload115Task(t *model.OfflineDownload115Task) error {
	if t == nil {
		return errors.New("115 Open offline download task is nil")
	}
	if t.TaskID == "" {
		return errors.New("115 Open offline download task ID is empty")
	}
	return nil
}

func CreateOfflineDownload115Task(t *model.OfflineDownload115Task) error {
	if err := validateOfflineDownload115Task(t); err != nil {
		return err
	}
	return errors.WithStack(db.Create(t).Error)
}

// SaveOfflineDownload115Task only updates existing, visible tasks. In particular,
// a worker's delayed snapshot cannot insert a missing task or undo its tombstone.
func SaveOfflineDownload115Task(t *model.OfflineDownload115Task) error {
	if err := validateOfflineDownload115Task(t); err != nil {
		return err
	}
	result := db.Model(&model.OfflineDownload115Task{}).
		Where("task_id = ? AND deleted_at IS NULL", t.TaskID).
		Select("*").Omit("task_id", "created_at", "deleted_at").Updates(t)
	if result.Error != nil {
		return errors.WithStack(result.Error)
	}
	if result.RowsAffected != 0 {
		return nil
	}
	// Some databases report zero changed rows for an unchanged snapshot, so do
	// not confuse that case with a missing or concurrently deleted task.
	stored, err := GetOfflineDownload115Task(t.TaskID)
	if err != nil {
		return err
	}
	if stored.DeletedAt != nil {
		return ErrOfflineDownload115TaskDeleted
	}
	return nil
}

// GetOfflineDownload115Task includes tombstones for deletion and migration
// checks. Use GetOfflineDownload115Tasks when restoring the visible task list.
func GetOfflineDownload115Task(taskID string) (*model.OfflineDownload115Task, error) {
	var t model.OfflineDownload115Task
	if err := db.Where("task_id = ?", taskID).First(&t).Error; err != nil {
		return nil, errors.WithStack(err)
	}
	return &t, nil
}

func GetOfflineDownload115Tasks() ([]model.OfflineDownload115Task, error) {
	var tasks []model.OfflineDownload115Task
	err := db.Where("deleted_at IS NULL").Order("created_at").Order("task_id").Find(&tasks).Error
	return tasks, errors.WithStack(err)
}

// MarkOfflineDownload115TaskDeleted must succeed before the in-memory task is
// removed. Keeping the row prevents stale JSON imports from reviving the task.
func MarkOfflineDownload115TaskDeleted(taskID string, at time.Time) error {
	if taskID == "" {
		return errors.New("115 Open offline download task ID is empty")
	}
	if at.IsZero() {
		return errors.New("115 Open offline download deletion time is empty")
	}
	result := db.Model(&model.OfflineDownload115Task{}).
		Where("task_id = ? AND deleted_at IS NULL", taskID).
		Updates(map[string]interface{}{"deleted_at": at, "updated_at": at})
	if result.Error != nil {
		return errors.WithStack(result.Error)
	}
	if result.RowsAffected != 0 {
		return nil
	}
	stored, err := GetOfflineDownload115Task(taskID)
	if err != nil {
		return err
	}
	if stored.DeletedAt == nil {
		return errors.New("failed to mark 115 Open offline download task as deleted")
	}
	return nil
}

// MigrateOfflineDownload115Tasks imports old 115 snapshots and removes them from
// the generic snapshot in one transaction, before any download worker starts.
// An existing receipt or tombstone always wins over a legacy snapshot.
func MigrateOfflineDownload115Tasks(records []model.OfflineDownload115Task, filteredJSON string) error {
	for i := range records {
		if err := validateOfflineDownload115Task(&records[i]); err != nil {
			return err
		}
	}
	return errors.WithStack(db.Transaction(func(tx *gorm.DB) error {
		if len(records) != 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "task_id"}},
				DoNothing: true,
			}).Create(&records).Error; err != nil {
				return err
			}
		}
		key := clause.Eq{Column: "key", Value: "download"}
		result := tx.Model(&model.TaskItem{}).Where(key).Update("persist_data", filteredJSON)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 0 {
			return nil
		}
		// TaskItem has no unique key. Check existence before creating it so an
		// unchanged MySQL UPDATE does not add a duplicate generic snapshot row.
		var count int64
		if err := tx.Model(&model.TaskItem{}).Where(key).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		return tx.Create(&model.TaskItem{Key: "download", PersistData: filteredJSON}).Error
	}))
}
