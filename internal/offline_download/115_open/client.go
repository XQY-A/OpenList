package _115_open

import (
	"context"
	"errors"
	"fmt"
	"time"

	_115_open "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"gorm.io/gorm"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

type Open115 struct {
}

func (o *Open115) Name() string {
	return "115 Open"
}

func (o *Open115) Items() []model.SettingItem {
	return nil
}

func (o *Open115) Run(task *tool.DownloadTask) error {
	return errs.NotSupport
}

func (o *Open115) Init() (string, error) {
	return "ok", nil
}

func (o *Open115) IsReady() bool {
	tempDir := setting.GetStr(conf.Pan115OpenTempDir)
	if tempDir == "" {
		return false
	}
	storage, _, err := op.GetStorageAndActualPath(tempDir)
	if err != nil {
		return false
	}
	if _, ok := storage.(*_115_open.Open115); !ok {
		return false
	}
	return true
}

func (o *Open115) AddURL(args *tool.AddUrlArgs) (string, error) {
	prepared, err := o.Prepare(args.Ctx, args.TempDir)
	if err != nil {
		return "", err
	}
	return prepared.Submit(args.Ctx, args.Url)
}

func (o *Open115) Prepare(ctx context.Context, tempDir string) (*tool.Open115Submission, error) {
	storage, actualPath, err := op.GetStorageAndActualPath(tempDir)
	if err != nil {
		return nil, err
	}
	driver115Open, ok := storage.(*_115_open.Open115)
	if !ok {
		return nil, fmt.Errorf("unsupported storage driver for offline download, only 115 Open is supported")
	}
	reference := tool.Open115Reference{
		StorageID: storage.GetStorage().ID,
		AccountID: driver115Open.AccountID(),
	}
	if reference.StorageID == 0 || reference.AccountID == "" {
		return nil, errors.New("115 Open storage has no verified account identity")
	}
	var parentDir model.Obj
	err = driver115Open.WithOfflineAccount(ctx, reference.AccountID, func() error {
		if err := op.MakeDir(ctx, storage, actualPath); err != nil {
			return err
		}
		var getErr error
		parentDir, getErr = op.GetUnwrap(ctx, storage, actualPath)
		return getErr
	})
	if err != nil {
		return nil, err
	}
	return &tool.Open115Submission{
		Reference: reference,
		Submit: func(ctx context.Context, url string) (string, error) {
			hash, err := driver115Open.AddOfflineTaskForAccount(ctx, reference.AccountID, url, parentDir)
			var rejected *_115_open.OfflineTaskRejectedError
			if errors.As(err, &rejected) {
				return "", &tool.SubmissionRejectedError{Err: err}
			}
			return hash, err
		},
	}, nil
}

func resolveReference(reference tool.Open115Reference) (*_115_open.Open115, error) {
	if reference.StorageID == 0 || reference.AccountID == "" {
		return nil, referenceConflict("cloud_reference_missing", "115 Open task has no reliable storage and account binding")
	}
	row, err := db.GetStorageById(reference.StorageID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, referenceConflict("storage_missing", "115 Open task storage no longer exists")
		}
		return nil, &tool.Open115ReferenceError{
			Reason: "storage_unavailable", Err: fmt.Errorf("failed to load 115 Open task storage: %w", err),
		}
	}
	if row.Disabled {
		return nil, referenceConflict("storage_disabled", "115 Open task storage is disabled")
	}
	if row.Driver != tool.Open115Name {
		return nil, referenceConflict("storage_changed", "115 Open task storage no longer uses the original driver")
	}
	storage, err := op.GetStorageByMountPath(row.MountPath)
	if err != nil {
		return nil, referenceConflict("storage_unavailable", "115 Open task storage is not loaded")
	}
	driver115Open, ok := storage.(*_115_open.Open115)
	if !ok || storage.GetStorage().ID != reference.StorageID {
		return nil, referenceConflict("storage_changed", "115 Open task storage no longer uses the original driver")
	}
	if driver115Open.AccountID() != reference.AccountID {
		return nil, referenceConflict("account_changed", "115 Open task storage account has changed or is unavailable")
	}
	return driver115Open, nil
}

func referenceConflict(reason, message string) *tool.Open115ReferenceError {
	return &tool.Open115ReferenceError{
		Reason: reason, Err: fmt.Errorf("%w: %s", tool.ErrTaskConflict, message),
	}
}

func (o *Open115) ValidateReference(reference tool.Open115Reference) error {
	_, err := resolveReference(reference)
	return err
}

func (o *Open115) Delete(ctx context.Context, reference tool.Open115Reference) error {
	storage, err := resolveReference(reference)
	if err != nil {
		return err
	}
	err = storage.DeleteOfflineTaskForAccount(ctx, reference.AccountID, reference.InfoHash)
	return offlineReferenceError(err)
}

func offlineReferenceError(err error) error {
	if errors.Is(err, _115_open.ErrOfflineAccountChanged) {
		return &tool.Open115ReferenceError{
			Reason: "account_changed", Err: fmt.Errorf("%w: %w", tool.ErrTaskConflict, err),
		}
	}
	return err
}

func (o *Open115) Lookup(ctx context.Context, reference tool.Open115Reference) (*tool.Open115TaskStatus, error) {
	storage, err := resolveReference(reference)
	if err != nil {
		return nil, err
	}
	cloudTask, err := storage.GetOfflineTaskForAccount(ctx, reference.AccountID, reference.InfoHash)
	if err != nil || cloudTask == nil {
		return nil, offlineReferenceError(err)
	}
	status := &tool.Open115TaskStatus{
		State: cloudTask.Status,
		Name:  cloudTask.Name,
		Status: tool.Status{
			Progress: float64(cloudTask.PercentDone), TotalBytes: cloudTask.Size,
			Status: cloudTask.GetStatus(), Completed: cloudTask.IsDone(),
		},
	}
	if cloudTask.IsFailed() {
		status.Err = errors.New(cloudTask.GetStatus())
	}
	return status, nil
}

func (o *Open115) Remove(task *tool.DownloadTask) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return o.Delete(ctx, task.Open115Reference())
}

func (o *Open115) Status(task *tool.DownloadTask) (*tool.Status, error) {
	ctx, cancel := context.WithTimeout(task.Ctx(), 30*time.Second)
	defer cancel()
	status, err := o.Lookup(ctx, task.Open115Reference())
	if err != nil {
		return nil, err
	}
	if status == nil {
		return &tool.Status{Status: "the task has been deleted", Err: errors.New("115 Open download record was not found")}, nil
	}
	return &status.Status, nil
}

var _ tool.Tool = (*Open115)(nil)
var _ tool.Open115Tool = (*Open115)(nil)

func init() {
	tool.Tools.Add(&Open115{})
}
