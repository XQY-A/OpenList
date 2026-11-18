package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/tache"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupDownloadManagerDB(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	dir := t.TempDir()
	d, err := gorm.Open(sqlite.Open(filepath.Join(dir, "downloads.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	previousConfig := conf.Conf
	conf.Conf = conf.DefaultConfig(dir)
	db.Init(d)
	t.Cleanup(func() {
		conf.Conf = previousConfig
		sqlDB, err := d.DB()
		if err != nil {
			t.Error(err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			t.Error(err)
		}
	})
	creator := &model.User{Username: "download-owner", BasePath: "/", Permission: 4}
	if err := db.CreateUser(creator); err != nil {
		t.Fatal(err)
	}
	return d, creator
}

func manager115Record(t *testing.T, id string, creatorID uint, state tache.State, submission string) model.OfflineDownload115Task {
	t.Helper()
	encoded, err := json.Marshal(download115Snapshot{
		URL: "magnet:?xt=urn:btih:example", DstDirPath: "/115/downloads", TempDir: "/115/downloads",
		DeletePolicy: DeleteNever, State: state, Status: "saved task", Progress: 25, TotalBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	record := model.OfflineDownload115Task{
		TaskID: id, Version: open115RecordVersion, CreatorID: creatorID,
		SubmissionState: submission, CloudDeleteState: "none",
		CreatedAt: now, UpdatedAt: now, Snapshot: string(encoded),
	}
	if submission == SubmissionAccepted {
		record.StorageID, record.AccountID, record.InfoHash = 7, "115-owner", "original-cloud-hash"
	}
	return record
}

func createManager115Record(t *testing.T, record model.OfflineDownload115Task) {
	t.Helper()
	if err := db.CreateOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
}

func manager115Snapshot(t *testing.T, id string) (model.OfflineDownload115Task, download115Snapshot) {
	t.Helper()
	record, err := db.GetOfflineDownload115Task(id)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot download115Snapshot
	if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
		t.Fatal(err)
	}
	return *record, snapshot
}

func TestDownloadTaskManagerRestores115WithoutGenericPersistence(t *testing.T) {
	_, creator := setupDownloadManagerDB(t)
	createManager115Record(t, manager115Record(t, "priority", creator.ID, tache.StateSucceeded, SubmissionAccepted))
	deleted := manager115Record(t, "deleted", creator.ID, tache.StateSucceeded, SubmissionAccepted)
	now := time.Now()
	deleted.DeletedAt = &now
	createManager115Record(t, deleted)
	ordinary := `{"id":"ordinary","toolname":"aria2","state":2,"future_field":"keep me"}`
	legacy := fmt.Sprintf(`{"id":"legacy","toolname":"115 Open","state":1,"url":"ed2k://old","temp_dir":"/115","dst_dir_path":"/115","Creator":{"id":%d,"username":"outdated","password":"must-not-be-copied"}}`, creator.ID)
	jsonData := `[` + ordinary + `,` + legacy + `,` + legacy + `,{"id":"priority","toolname":"115 Open","state":0},{"id":"deleted","toolname":"115 Open","state":0}]`
	if err := db.CreateTaskData(&model.TaskItem{Key: "download", PersistData: jsonData}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewDownloadTaskManager(2, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(manager.GetAll()); got != 2 {
		t.Fatalf("restored %d tasks, want priority and legacy only", got)
	}
	priority, ok := manager.GetByID("priority")
	if !ok || priority.Open115Reference().InfoHash != "original-cloud-hash" || priority.GetState() != tache.StateSucceeded {
		t.Fatalf("durable receipt did not win over legacy snapshot: %+v", priority)
	}
	if _, ok := manager.GetByID("deleted"); ok {
		t.Fatal("legacy JSON revived a tombstone")
	}
	if _, ok := manager.GetByID("ordinary"); ok {
		t.Fatal("generic persistence was disabled but another tool was restored")
	}
	imported, ok := manager.GetByID("legacy")
	if !ok || imported.Open115SubmissionState() != SubmissionLegacy || imported.GetState() != tache.StateFailed || imported.Retryable() {
		t.Fatalf("legacy task must remain visible without becoming runnable: %+v", imported)
	}
	if imported.GetCreator() == nil || imported.GetCreator().Username != creator.Username {
		t.Fatal("legacy creator was not reloaded from the users table")
	}
	record, _ := manager115Snapshot(t, "legacy")
	if strings.Contains(record.Snapshot, "must-not-be-copied") || strings.Contains(record.Snapshot, "Creator") || strings.Contains(record.Snapshot, "username") {
		t.Fatalf("legacy import copied a user into the durable snapshot: %s", record.Snapshot)
	}
	filtered, err := db.GetTaskDataByType("download")
	if err != nil || filtered.PersistData != "["+ordinary+"]" {
		t.Fatalf("ordinary JSON was not preserved: %+v, %v", filtered, err)
	}
	// Repeated recovery must retain the same imported ID and respect the
	// existing tombstone even after the original JSON has been filtered.
	recovered, err := NewDownloadTaskManager(2, 3, false)
	if err != nil || len(recovered.GetAll()) != 2 {
		t.Fatalf("second recovery duplicated or lost tasks: %v", err)
	}
}

func TestDownloadTaskManagerNormalizesInterruptedStates(t *testing.T) {
	_, creator := setupDownloadManagerDB(t)
	cases := []struct {
		name       string
		state      tache.State
		submission string
		change     func(*model.OfflineDownload115Task)
		wantState  tache.State
		wantSubmit string
	}{
		{name: "pending", state: tache.StatePending, submission: SubmissionPending, wantState: tache.StatePending, wantSubmit: SubmissionPending},
		{name: "accepted running", state: tache.StateRunning, submission: SubmissionAccepted, wantState: tache.StatePending, wantSubmit: SubmissionAccepted},
		{name: "accepted errored", state: tache.StateErrored, submission: SubmissionAccepted, wantState: tache.StatePending, wantSubmit: SubmissionAccepted},
		{name: "waiting retry", state: tache.StateWaitingRetry, submission: SubmissionAccepted, wantState: tache.StatePending, wantSubmit: SubmissionAccepted},
		{name: "before retry", state: tache.StateBeforeRetry, submission: SubmissionAccepted, wantState: tache.StatePending, wantSubmit: SubmissionAccepted},
		{name: "submitting", state: tache.StateRunning, submission: SubmissionSubmitting, wantState: tache.StateFailed, wantSubmit: SubmissionUnknown},
		{name: "unknown", state: tache.StateRunning, submission: SubmissionUnknown, wantState: tache.StateFailed, wantSubmit: SubmissionUnknown},
		{name: "legacy", state: tache.StatePending, submission: SubmissionLegacy, wantState: tache.StateFailed, wantSubmit: SubmissionLegacy},
		{name: "missing receipt", state: tache.StateRunning, submission: SubmissionAccepted, change: func(r *model.OfflineDownload115Task) { r.InfoHash = "" }, wantState: tache.StateFailed, wantSubmit: SubmissionUnknown},
		{name: "missing binding", state: tache.StateRunning, submission: SubmissionAccepted, change: func(r *model.OfflineDownload115Task) { r.AccountID = "" }, wantState: tache.StateFailed, wantSubmit: SubmissionLegacy},
		{name: "queued cancellation", state: tache.StateCanceling, submission: SubmissionPending, wantState: tache.StateCanceled, wantSubmit: SubmissionPending},
		{name: "failure transition", state: tache.StateFailing, submission: SubmissionAccepted, wantState: tache.StateFailed, wantSubmit: SubmissionAccepted},
		{name: "succeeded", state: tache.StateSucceeded, submission: SubmissionAccepted, wantState: tache.StateSucceeded, wantSubmit: SubmissionAccepted},
		{name: "failed", state: tache.StateFailed, submission: SubmissionAccepted, wantState: tache.StateFailed, wantSubmit: SubmissionAccepted},
		{name: "canceled", state: tache.StateCanceled, submission: SubmissionAccepted, wantState: tache.StateCanceled, wantSubmit: SubmissionAccepted},
		{name: "deleted creator", state: tache.StatePending, submission: SubmissionPending, change: func(r *model.OfflineDownload115Task) { r.CreatorID = creator.ID + 100 }, wantState: tache.StateFailed, wantSubmit: SubmissionPending},
		{name: "interrupted cleanup", state: tache.StateRunning, submission: SubmissionAccepted, change: func(r *model.OfflineDownload115Task) { r.CloudDeleteState = "deleting" }, wantState: tache.StateFailed, wantSubmit: SubmissionAccepted},
	}
	for _, tc := range cases {
		record := manager115Record(t, tc.name, creator.ID, tc.state, tc.submission)
		if tc.change != nil {
			tc.change(&record)
		}
		createManager115Record(t, record)
	}
	manager, err := NewDownloadTaskManager(2, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			download, ok := manager.GetByID(tc.name)
			if !ok {
				t.Fatal("task missing from manager")
			}
			if download.GetState() != tc.wantState || download.Open115SubmissionState() != tc.wantSubmit {
				t.Fatalf("restored state/submission = %d/%s, want %d/%s", download.GetState(), download.Open115SubmissionState(), tc.wantState, tc.wantSubmit)
			}
			record, snapshot := manager115Snapshot(t, tc.name)
			if snapshot.State != tc.wantState || record.SubmissionState != tc.wantSubmit {
				t.Fatalf("normalization was not persisted: state=%d, submission=%s", snapshot.State, record.SubmissionState)
			}
			if isDownloadTerminal(tc.wantState) && snapshot.EndTime == nil {
				t.Fatal("terminal recovery did not persist an end time")
			}
			if tc.wantSubmit == SubmissionUnknown || tc.wantSubmit == SubmissionLegacy {
				if download.Retryable() {
					t.Fatal("unsafe submission state allowed retry")
				}
			}
		})
	}
}

type manager115Tool struct {
	prepares atomic.Int64
	lookups  chan Open115Reference
}

func (*manager115Tool) Name() string               { return Open115Name }
func (*manager115Tool) Items() []model.SettingItem { return nil }
func (*manager115Tool) Init() (string, error)      { return "ok", nil }
func (*manager115Tool) IsReady() bool              { return true }
func (*manager115Tool) Run(*DownloadTask) error    { return errs.NotSupport }
func (*manager115Tool) AddURL(*AddUrlArgs) (string, error) {
	return "", errors.New("legacy submission must not be used")
}
func (*manager115Tool) Remove(*DownloadTask) error { return nil }
func (*manager115Tool) Status(*DownloadTask) (*Status, error) {
	return nil, errors.New("legacy status must not be used")
}
func (m *manager115Tool) Prepare(context.Context, string) (*Open115Submission, error) {
	m.prepares.Add(1)
	return nil, errors.New("known receipt must not be submitted again")
}
func (m *manager115Tool) Lookup(_ context.Context, reference Open115Reference) (*Open115TaskStatus, error) {
	m.lookups <- reference
	return &Open115TaskStatus{Status: Status{Completed: true, Progress: 100, Status: "complete"}, State: 2}, nil
}
func (*manager115Tool) Delete(context.Context, Open115Reference, bool) error { return nil }
func (*manager115Tool) ValidateReference(Open115Reference) error             { return nil }

func TestDownloadTaskManagerKeepsGenericPersistenceAndResumesReceipt(t *testing.T) {
	d, creator := setupDownloadManagerDB(t)
	createManager115Record(t, manager115Record(t, "accepted", creator.ID, tache.StateRunning, SubmissionAccepted))
	createManager115Record(t, manager115Record(t, "unknown", creator.ID, tache.StateRunning, SubmissionUnknown))
	createManager115Record(t, manager115Record(t, "terminal", creator.ID, tache.StateSucceeded, SubmissionAccepted))
	deleted := manager115Record(t, "deleted", creator.ID, tache.StateSucceeded, SubmissionAccepted)
	now := time.Now()
	deleted.DeletedAt = &now
	createManager115Record(t, deleted)
	ordinary := `{"id":"ordinary","toolname":"aria2","state":2}`
	source := `[` + ordinary + `,` + ordinary + `,{"id":"deleted","toolname":"aria2","state":2},{"id":"accepted","toolname":"115 Open","state":1}]`
	if err := db.CreateTaskData(&model.TaskItem{Key: "download", PersistData: source}); err != nil {
		t.Fatal(err)
	}
	client := &manager115Tool{lookups: make(chan Open115Reference, 4)}
	previous, existed := Tools[Open115Name]
	Tools[Open115Name] = client
	t.Cleanup(func() {
		if existed {
			Tools[Open115Name] = previous
		} else {
			delete(Tools, Open115Name)
		}
	})
	manager, err := NewDownloadTaskManager(2, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the optional generic snapshot's actual debounced write. This
	// also keeps its callback within this database fixture's lifetime.
	persisted := make(chan struct{}, 1)
	if err := d.Callback().Update().After("gorm:update").Register("test:download_snapshot", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "TaskItem" {
			select {
			case persisted <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-persisted:
		case <-time.After(5 * time.Second):
			t.Error("generic snapshot callback did not finish")
		}
	})
	if got := len(manager.GetAll()); got != 4 {
		t.Fatalf("restored %d tasks, want three durable tasks and one generic task", got)
	}
	accepted, ok := manager.GetByID("accepted")
	if !ok || accepted.GetState() != tache.StatePending || client.prepares.Load() != 0 || len(client.lookups) != 0 {
		t.Fatal("constructor started or lost the accepted task")
	}
	manager.Start()
	manager.Start()
	select {
	case reference := <-client.lookups:
		if reference.InfoHash != "original-cloud-hash" || reference.StorageID != 7 || reference.AccountID != "115-owner" {
			t.Fatalf("resume queried a different receipt: %+v", reference)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("accepted task was not resumed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for accepted.GetState() != tache.StateSucceeded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if accepted.GetState() != tache.StateSucceeded || client.prepares.Load() != 0 {
		t.Fatal("resumed receipt did not finish without resubmission")
	}
	select {
	case <-persisted:
		// Cleanup also consumes a token so it can always ensure the callback
		// has completed, including after an earlier assertion fails.
		persisted <- struct{}{}
	case <-time.After(5 * time.Second):
		t.Fatal("generic task persistence did not run")
	}
	if len(client.lookups) != 0 {
		t.Fatal("terminal or unknown task was enqueued during recovery")
	}
	item, err := db.GetTaskDataByType("download")
	if err != nil {
		t.Fatal(err)
	}
	var saved []struct {
		ID       string `json:"id"`
		Toolname string `json:"toolname"`
	}
	if err := json.Unmarshal([]byte(item.PersistData), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].ID != "ordinary" || saved[0].Toolname != "aria2" {
		t.Fatalf("generic persistence included duplicate, durable, or deleted tasks: %s", item.PersistData)
	}
	_, snapshot := manager115Snapshot(t, "accepted")
	if snapshot.State != tache.StateSucceeded {
		t.Fatalf("resumed terminal state was not persisted: %d", snapshot.State)
	}
}

func TestDownloadTaskManagerFailsClosedOnRecoveryErrors(t *testing.T) {
	for _, name := range []string{"generic read", "invalid JSON", "migration write", "durable read", "creator read", "normalization write", "unsupported version", "null snapshot"} {
		t.Run(name, func(t *testing.T) {
			d, creator := setupDownloadManagerDB(t)
			record := manager115Record(t, "stored", creator.ID, tache.StateRunning, SubmissionSubmitting)
			source := "[]"
			switch name {
			case "invalid JSON":
				source = "["
			case "migration write":
				source = `[{"id":"legacy","toolname":"115 Open","state":1}]`
			case "unsupported version":
				record.Version++
			case "null snapshot":
				record.Snapshot = "null"
			}
			createManager115Record(t, record)
			if err := db.CreateTaskData(&model.TaskItem{Key: "download", PersistData: source}); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "generic read":
				if err := d.Migrator().DropTable(&model.TaskItem{}); err != nil {
					t.Fatal(err)
				}
			case "durable read":
				if err := d.Migrator().DropTable(&model.OfflineDownload115Task{}); err != nil {
					t.Fatal(err)
				}
			case "creator read":
				if err := d.Migrator().DropTable(&model.User{}); err != nil {
					t.Fatal(err)
				}
			case "migration write", "normalization write":
				target := "TaskItem"
				if name == "normalization write" {
					target = "OfflineDownload115Task"
				}
				if err := d.Callback().Update().Before("gorm:update").Register("test:fail_recovery", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == target {
						tx.AddError(errors.New("injected recovery write failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			manager, err := NewDownloadTaskManager(2, 2, true)
			if err == nil || manager != nil {
				t.Fatalf("recovery error returned a runnable manager: %v", err)
			}
			if name == "migration write" {
				if _, err := db.GetOfflineDownload115Task("legacy"); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("failed migration left a partial imported row: %v", err)
				}
				item, err := db.GetTaskDataByType("download")
				if err != nil || item.PersistData != source {
					t.Fatalf("failed migration changed the original JSON: %+v, %v", item, err)
				}
			}
			if name == "normalization write" {
				stored, err := db.GetOfflineDownload115Task("stored")
				if err != nil || stored.SubmissionState != SubmissionSubmitting {
					t.Fatalf("failed normalization changed durable submission state: %+v, %v", stored, err)
				}
			}
		})
	}
}
