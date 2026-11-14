package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewDownloadTaskManager restores downloads without starting workers. The
// caller must wait for storage loading and initialize the transfer manager
// before calling Start. Critical recovery errors are returned rather than
// passed to tache's recovery callback, which only logs them.
func NewDownloadTaskManager(workers, maxRetry int, persistent bool) (*tache.Manager[*DownloadTask], error) {
	ordinary, err := migrateLegacy115Downloads()
	if err != nil {
		return nil, err
	}
	records, err := db.GetOfflineDownload115Tasks()
	if err != nil {
		return nil, fmt.Errorf("failed to load 115 Open downloads: %w", err)
	}

	downloads := make([]*DownloadTask, 0, len(records)+len(ordinary))
	seen := make(map[string]struct{}, len(records)+len(ordinary))
	for i := range records {
		download, err := restore115Download(records[i])
		if err != nil {
			return nil, fmt.Errorf("failed to restore 115 Open task %s: %w", records[i].TaskID, err)
		}
		seen[download.GetID()] = struct{}{}
		downloads = append(downloads, download)
	}
	if persistent {
		for _, download := range ordinary {
			id := download.GetID()
			if _, exists := seen[id]; exists {
				continue
			}
			// A durable tombstone must also win over a stale generic entry,
			// including an entry whose tool name differs from the durable row.
			if _, err := db.GetOfflineDownload115Task(id); err == nil {
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed to check download %s during recovery: %w", id, err)
			}
			seen[id] = struct{}{}
			downloads = append(downloads, download)
		}
	}

	opts := []tache.Option{
		tache.WithWorks(max(workers, 0)),
		tache.WithMaxRetry(maxRetry),
		tache.WithRunning(false),
	}
	if persistent {
		// All entries were validated and deduplicated above. Giving tache
		// the old snapshot would enqueue them a second time during New.
		opts = append(opts, tache.WithPersistFunction(func() ([]byte, error) {
			return []byte("[]"), nil
		}, db.UpdateTaskDataFunc("download", true)))
	}
	manager := tache.NewManager[*DownloadTask](opts...)
	for _, download := range downloads {
		manager.Add(download)
	}
	return manager, nil
}

func migrateLegacy115Downloads() ([]*DownloadTask, error) {
	item, err := db.GetTaskDataByType("download")
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to read legacy download snapshot: %w", err)
	}
	var entries []json.RawMessage
	if item != nil && strings.TrimSpace(item.PersistData) != "" {
		if err := json.Unmarshal([]byte(item.PersistData), &entries); err != nil {
			return nil, fmt.Errorf("invalid legacy download snapshot: %w", err)
		}
	}
	filtered := make([]json.RawMessage, 0, len(entries))
	ordinary := make([]*DownloadTask, 0, len(entries))
	legacy := make([]model.OfflineDownload115Task, 0)
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		var download *DownloadTask
		if err := json.Unmarshal(entry, &download); err != nil {
			return nil, fmt.Errorf("invalid legacy download at index %d: %w", i, err)
		}
		if download == nil {
			return nil, fmt.Errorf("invalid null download at index %d", i)
		}
		if !download.Is115Open() {
			// Keep the original bytes, including fields unknown to this
			// version, for every other tool's optional persistence.
			filtered = append(filtered, entry)
			if download.GetID() == "" {
				download.Base.SetID(uuid.NewString())
			}
			ordinary = append(ordinary, download)
			continue
		}
		if download.GetID() == "" {
			download.Base.SetID(uuid.NewString())
		}
		id := download.GetID()
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if !isDownloadTerminal(download.Base.GetState()) && download.Base.GetState() != tache.StateCanceling {
			download.Base.SetState(tache.StateFailed)
			download.Base.SetErr(errors.New("historical 115 Open task has no reliable cloud reference"))
		}
		snapshot, err := json.Marshal(download.snapshot115Locked())
		if err != nil {
			return nil, fmt.Errorf("failed to convert legacy 115 Open task %s: %w", id, err)
		}
		now := time.Now()
		record := model.OfflineDownload115Task{
			TaskID: id, Version: open115RecordVersion,
			SubmissionState: SubmissionLegacy, CloudDeleteState: "none",
			CreatedAt: now, UpdatedAt: now, Snapshot: string(snapshot),
		}
		if download.Creator != nil {
			record.CreatorID = download.Creator.ID
		}
		legacy = append(legacy, record)
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return nil, fmt.Errorf("failed to filter legacy download snapshot: %w", err)
	}
	if err := db.MigrateOfflineDownload115Tasks(legacy, string(data)); err != nil {
		return nil, fmt.Errorf("failed to migrate legacy 115 Open downloads: %w", err)
	}
	return ordinary, nil
}

func restore115Download(record model.OfflineDownload115Task) (*DownloadTask, error) {
	if record.Version != open115RecordVersion {
		return nil, fmt.Errorf("unsupported 115 Open task record version %d", record.Version)
	}
	if record.TaskID == "" {
		return nil, errors.New("115 Open task ID is empty")
	}
	var snapshot *download115Snapshot
	if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
		return nil, fmt.Errorf("invalid 115 Open task snapshot: %w", err)
	}
	if snapshot == nil {
		return nil, errors.New("115 Open task snapshot is null")
	}
	var creator *model.User
	if record.CreatorID != 0 {
		var err error
		creator, err = db.GetUserById(record.CreatorID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to load download creator: %w", err)
		}
	}
	download := &DownloadTask{
		TaskExtension: task.TaskExtension{
			Creator: creator, ApiUrl: snapshot.APIURL,
			StartTime: copyTime(snapshot.StartTime), EndTime: copyTime(snapshot.EndTime),
			TotalBytes: snapshot.TotalBytes,
		},
		Url: snapshot.URL, DstDirPath: snapshot.DstDirPath, TempDir: snapshot.TempDir,
		DeletePolicy: snapshot.DeletePolicy, Toolname: Open115Name,
		Status: snapshot.Status, GID: record.InfoHash, open115: &record,
	}
	download.Base.SetID(record.TaskID)
	download.Base.SetState(snapshot.State)
	download.Base.SetProgress(snapshot.Progress)
	if snapshot.Error != "" {
		download.Base.SetErr(errors.New(snapshot.Error))
	}
	if record.CloudDeleteState == "" {
		record.CloudDeleteState = CloudDeleteNone
	} else if record.CloudDeleteState == CloudDeleteDeleting {
		// The process may have stopped after dispatch, before receiving or
		// persisting the deletion response. Only an explicit, fresh query can
		// reconcile it; recovery must never send another cloud mutation.
		record.CloudDeleteState = CloudDeleteUnknown
	}
	if record.InfoHash != "" {
		if record.StorageID == 0 || record.AccountID == "" {
			record.SubmissionState = SubmissionLegacy
		} else if record.SubmissionState != SubmissionLegacy {
			record.SubmissionState = SubmissionAccepted
		}
	} else {
		switch record.SubmissionState {
		case SubmissionPending, SubmissionFailed, SubmissionUnknown, SubmissionLegacy:
		case SubmissionSubmitting, SubmissionAccepted:
			record.SubmissionState = SubmissionUnknown
		default:
			record.SubmissionState = SubmissionUnknown
		}
	}
	// Normalize interrupted local transitions before Add; tache deliberately
	// does not enqueue Errored tasks, whereas a known receipt can be polled.
	switch download.Base.GetState() {
	case tache.StateCanceling:
		download.Base.SetState(tache.StateCanceled)
		download.Base.SetErr(context.Canceled)
	case tache.StateFailing:
		download.Base.SetState(tache.StateFailed)
	}
	if !isDownloadTerminal(download.Base.GetState()) {
		var reason string
		switch {
		case record.SubmissionState == SubmissionUnknown:
			reason = "115 Open submission result is unknown; verify the cloud task before creating a new download"
		case record.SubmissionState == SubmissionLegacy:
			reason = "historical 115 Open task has no reliable cloud reference"
		case record.CloudDeleteState != "none":
			reason = "115 Open task cleanup was interrupted; retry deletion"
		case creator == nil:
			reason = "115 Open task creator no longer exists"
		case snapshot.URL == "" || snapshot.DstDirPath == "" || snapshot.TempDir == "":
			reason = "115 Open task snapshot is missing its download URL or paths"
		case snapshot.State < tache.StatePending || snapshot.State > tache.StateBeforeRetry:
			reason = "115 Open task has an invalid execution state"
		}
		if reason != "" {
			download.Base.SetState(tache.StateFailed)
			download.Base.SetErr(errors.New(reason))
		} else {
			download.Base.SetState(tache.StatePending)
		}
	}
	if isDownloadTerminal(download.Base.GetState()) && download.EndTime == nil {
		now := time.Now()
		download.EndTime = &now
	}
	// Save the normalized outcome before the manager can expose or schedule
	// it. A failed write must not leave an apparently successful recovery.
	if err := download.save115(); err != nil {
		return nil, err
	}
	return download, nil
}
