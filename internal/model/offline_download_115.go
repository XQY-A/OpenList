package model

import "time"

// OfflineDownload115Task keeps the cloud receipt independently of the optional
// generic task snapshot. Storage and creator IDs intentionally have no foreign
// keys: removing a mount or user must not discard the only cloud-task reference.
type OfflineDownload115Task struct {
	TaskID           string     `json:"task_id" gorm:"size:64;primaryKey"`
	Version          int        `json:"version"`
	CreatorID        uint       `json:"creator_id"`
	StorageID        uint       `json:"storage_id"`
	AccountID        string     `json:"account_id" gorm:"size:32"`
	InfoHash         string     `json:"info_hash" gorm:"size:128"`
	SubmissionState  string     `json:"submission_state" gorm:"size:16"`
	CloudDeleteState string     `json:"cloud_delete_state" gorm:"size:16"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	AcceptedAt       *time.Time `json:"accepted_at"`
	CloudDeletedAt   *time.Time `json:"cloud_deleted_at"`
	DeletedAt        *time.Time `json:"deleted_at"`
	Snapshot         string     `json:"snapshot" gorm:"type:text"`
}
