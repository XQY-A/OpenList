package db

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func setupOfflineDownload115DB(t *testing.T) *gorm.DB {
	t.Helper()
	d := openTestDB(t, "ol_")
	conn, err := d.DB()
	if err != nil {
		t.Fatal(err)
	}
	// An in-memory SQLite database belongs to one connection.
	conn.SetMaxOpenConns(1)
	previous := db
	db = d
	t.Cleanup(func() {
		db = previous
		_ = conn.Close()
	})
	if err := d.AutoMigrate(&model.OfflineDownload115Task{}, &model.TaskItem{}); err != nil {
		t.Fatal(err)
	}
	return d
}

func offlineDownload115Record(taskID string) model.OfflineDownload115Task {
	now := time.Date(2026, time.November, 14, 10, 0, 0, 0, time.UTC)
	return model.OfflineDownload115Task{
		TaskID:           taskID,
		Version:          1,
		CreatorID:        3,
		StorageID:        12,
		AccountID:        "115001",
		InfoHash:         "Cloud-ReCeIpT:+/=",
		SubmissionState:  "accepted",
		CloudDeleteState: "pending",
		CreatedAt:        now,
		UpdatedAt:        now,
		AcceptedAt:       &now,
		Snapshot:         `{"url":"ed2k://test","state":1}`,
	}
}

func TestOfflineDownload115SaveReceiptAndZeroValues(t *testing.T) {
	setupOfflineDownload115DB(t)
	record := offlineDownload115Record("receipt")
	if err := CreateOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
	got, err := GetOfflineDownload115Task(record.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InfoHash != record.InfoHash || got.AccountID != record.AccountID || got.StorageID != record.StorageID || got.CreatorID != record.CreatorID {
		t.Fatalf("receipt or binding changed on round trip: %+v", got)
	}
	if got.Snapshot != record.Snapshot || got.AcceptedAt == nil || !got.AcceptedAt.Equal(*record.AcceptedAt) {
		t.Fatalf("receipt snapshot or timestamp changed: %+v", got)
	}

	// Updates must include explicit zero values, without overwriting creation
	// time or allowing a caller to bypass the dedicated tombstone operation.
	createdAt := record.CreatedAt
	deletedAt := createdAt.Add(time.Hour)
	record.CreatorID = 0
	record.StorageID = 0
	record.AccountID = ""
	record.InfoHash = ""
	record.SubmissionState = "pending"
	record.CloudDeleteState = ""
	record.CreatedAt = time.Time{}
	record.AcceptedAt = nil
	record.CloudDeletedAt = nil
	record.DeletedAt = &deletedAt
	record.Snapshot = ""
	if err := SaveOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
	got, err = GetOfflineDownload115Task(record.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatorID != 0 || got.StorageID != 0 || got.AccountID != "" || got.InfoHash != "" || got.CloudDeleteState != "" || got.Snapshot != "" || got.AcceptedAt != nil {
		t.Fatalf("zero values were not saved: %+v", got)
	}
	if got.SubmissionState != "pending" || !got.CreatedAt.Equal(createdAt) || got.DeletedAt != nil {
		t.Fatalf("save changed immutable fields or failed to update state: %+v", got)
	}
	if err := SaveOfflineDownload115Task(got); err != nil {
		t.Fatalf("unchanged save failed: %v", err)
	}
}

func TestOfflineDownload115SaveDoesNotCreateMissingTask(t *testing.T) {
	setupOfflineDownload115DB(t)
	record := offlineDownload115Record("missing")
	if err := SaveOfflineDownload115Task(&record); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("save missing task error = %v, want record not found", err)
	}
	if _, err := GetOfflineDownload115Task(record.TaskID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("save created missing task: %v", err)
	}
	if err := MarkOfflineDownload115TaskDeleted(record.TaskID, time.Now()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("delete missing task error = %v, want record not found", err)
	}
}

func TestOfflineDownload115TombstoneSurvivesLateSave(t *testing.T) {
	setupOfflineDownload115DB(t)
	record := offlineDownload115Record("deleted")
	if err := CreateOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
	deletedAt := record.CreatedAt.Add(time.Hour)
	if err := MarkOfflineDownload115TaskDeleted(record.TaskID, deletedAt); err != nil {
		t.Fatal(err)
	}
	// The worker still holds the pre-deletion snapshot while a local delete has
	// already returned. It must not make the task visible again.
	record.Snapshot = `{"state":2}`
	if err := SaveOfflineDownload115Task(&record); !errors.Is(err, ErrOfflineDownload115TaskDeleted) {
		t.Fatalf("late save error = %v, want deleted task", err)
	}
	if err := CreateOfflineDownload115Task(&record); err == nil {
		t.Fatal("duplicate create replaced the tombstone")
	}
	if err := MarkOfflineDownload115TaskDeleted(record.TaskID, deletedAt.Add(time.Hour)); err != nil {
		t.Fatalf("repeated delete is not idempotent: %v", err)
	}
	got, err := GetOfflineDownload115Task(record.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DeletedAt == nil || !got.DeletedAt.Equal(deletedAt) || got.InfoHash != record.InfoHash || got.Snapshot == record.Snapshot {
		t.Fatalf("tombstone or receipt changed after late write: %+v", got)
	}
	visible, err := GetOfflineDownload115Tasks()
	if err != nil || len(visible) != 0 {
		t.Fatalf("deleted task restored into visible list: tasks=%+v, err=%v", visible, err)
	}
}

func TestOfflineDownload115WriteFailureKeepsReceiptVisible(t *testing.T) {
	d := setupOfflineDownload115DB(t)
	record := offlineDownload115Record("write-failure")
	if err := CreateOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
	table := quoteIdent(d.NamingStrategy.TableName("OfflineDownload115Task"))
	if err := d.Exec("CREATE TRIGGER reject_receipt_update BEFORE UPDATE ON " + table +
		" BEGIN SELECT RAISE(ABORT, 'receipt write failed'); END").Error; err != nil {
		t.Fatal(err)
	}
	record.CloudDeleteState = "deleted"
	if err := SaveOfflineDownload115Task(&record); err == nil || !strings.Contains(err.Error(), "receipt write failed") {
		t.Fatalf("save error=%v, want injected failure", err)
	}
	if err := MarkOfflineDownload115TaskDeleted(record.TaskID, time.Now()); err == nil || !strings.Contains(err.Error(), "receipt write failed") {
		t.Fatalf("delete error=%v, want injected failure", err)
	}
	visible, err := GetOfflineDownload115Tasks()
	if err != nil || len(visible) != 1 || visible[0].InfoHash != record.InfoHash || visible[0].CloudDeleteState != "pending" {
		t.Fatalf("failed write discarded or changed the receipt: tasks=%+v, err=%v", visible, err)
	}
}

func TestOfflineDownload115ConcurrentSaveCannotUndoDeletion(t *testing.T) {
	setupOfflineDownload115DB(t)
	record := offlineDownload115Record("concurrent")
	if err := CreateOfflineDownload115Task(&record); err != nil {
		t.Fatal(err)
	}
	const workers = 4
	results := make(chan error, workers+1)
	start := make(chan struct{})
	for range workers {
		go func() {
			<-start
			for range 8 {
				snapshot := record
				if err := SaveOfflineDownload115Task(&snapshot); err != nil && !errors.Is(err, ErrOfflineDownload115TaskDeleted) {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	go func() {
		<-start
		results <- MarkOfflineDownload115TaskDeleted(record.TaskID, time.Now())
	}()
	close(start)
	for range workers + 1 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	got, err := GetOfflineDownload115Task(record.TaskID)
	if err != nil || got.DeletedAt == nil {
		t.Fatalf("concurrent writes undid deletion: record=%+v, err=%v", got, err)
	}
}

func TestOfflineDownload115MigrationKeepsReceiptsAndTombstones(t *testing.T) {
	d := setupOfflineDownload115DB(t)
	accepted := offlineDownload115Record("accepted")
	deleted := offlineDownload115Record("deleted")
	for _, record := range []*model.OfflineDownload115Task{&accepted, &deleted} {
		if err := CreateOfflineDownload115Task(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := MarkOfflineDownload115TaskDeleted(deleted.TaskID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := CreateTaskData(&model.TaskItem{Key: "download", PersistData: `[{"toolname":"115 Open"},{"toolname":"aria2"}]`}); err != nil {
		t.Fatal(err)
	}
	if err := CreateTaskData(&model.TaskItem{Key: "copy", PersistData: `[{"id":"copy"}]`}); err != nil {
		t.Fatal(err)
	}

	records := []model.OfflineDownload115Task{
		{TaskID: accepted.TaskID, Version: 1, SubmissionState: "legacy", Snapshot: "legacy accepted"},
		{TaskID: deleted.TaskID, Version: 1, SubmissionState: "legacy", Snapshot: "legacy deleted"},
		{TaskID: "legacy", Version: 1, CreatorID: 8, SubmissionState: "legacy", Snapshot: "legacy task"},
		{TaskID: "legacy", Version: 1, SubmissionState: "legacy", Snapshot: "duplicate legacy task"},
	}
	filteredJSON := `[{"toolname":"aria2"}]`
	for range 2 {
		if err := MigrateOfflineDownload115Tasks(records, filteredJSON); err != nil {
			t.Fatal(err)
		}
	}
	got, err := GetOfflineDownload115Task(accepted.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InfoHash != accepted.InfoHash || got.SubmissionState != "accepted" || got.Snapshot != accepted.Snapshot {
		t.Fatalf("legacy migration overwrote a receipt: %+v", got)
	}
	got, err = GetOfflineDownload115Task(deleted.TaskID)
	if err != nil || got.DeletedAt == nil {
		t.Fatalf("legacy migration revived a tombstone: record=%+v, err=%v", got, err)
	}
	got, err = GetOfflineDownload115Task("legacy")
	if err != nil || got.CreatorID != 8 || got.Snapshot != "legacy task" {
		t.Fatalf("duplicate migration replaced original legacy row: record=%+v, err=%v", got, err)
	}
	visible, err := GetOfflineDownload115Tasks()
	if err != nil || len(visible) != 2 {
		t.Fatalf("visible imported tasks = %+v, err=%v", visible, err)
	}
	var recordCount int64
	if err := d.Model(&model.OfflineDownload115Task{}).Count(&recordCount).Error; err != nil || recordCount != 3 {
		t.Fatalf("imported row count=%d, err=%v", recordCount, err)
	}
	download, err := GetTaskDataByType("download")
	if err != nil || download.PersistData != filteredJSON {
		t.Fatalf("generic download snapshot not filtered: record=%+v, err=%v", download, err)
	}
	copySnapshot, err := GetTaskDataByType("copy")
	if err != nil || copySnapshot.PersistData != `[{"id":"copy"}]` {
		t.Fatalf("unrelated task snapshot changed: record=%+v, err=%v", copySnapshot, err)
	}
	var snapshotCount int64
	if err := d.Model(&model.TaskItem{}).Where("key = ?", "download").Count(&snapshotCount).Error; err != nil || snapshotCount != 1 {
		t.Fatalf("repeated migration duplicated generic snapshot: count=%d, err=%v", snapshotCount, err)
	}
}

func TestOfflineDownload115MigrationRollsBackWhenSnapshotWriteFails(t *testing.T) {
	d := setupOfflineDownload115DB(t)
	oldJSON := `[{"id":"legacy","toolname":"115 Open"}]`
	if err := CreateTaskData(&model.TaskItem{Key: "download", PersistData: oldJSON}); err != nil {
		t.Fatal(err)
	}
	if err := d.Exec(`CREATE TRIGGER reject_download_snapshot_update
		BEFORE UPDATE ON ol_task_items
		BEGIN SELECT RAISE(ABORT, 'snapshot write failed'); END`).Error; err != nil {
		t.Fatal(err)
	}
	records := []model.OfflineDownload115Task{{TaskID: "legacy", Version: 1, SubmissionState: "legacy"}}
	if err := MigrateOfflineDownload115Tasks(records, "[]"); err == nil || !strings.Contains(err.Error(), "snapshot write failed") {
		t.Fatalf("migration error=%v, want injected failure", err)
	}
	if _, err := GetOfflineDownload115Task("legacy"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("import survived failed snapshot migration: %v", err)
	}
	got, err := GetTaskDataByType("download")
	if err != nil || got.PersistData != oldJSON {
		t.Fatalf("failed migration lost old task snapshot: record=%+v, err=%v", got, err)
	}
}

func TestOfflineDownload115MigrationCreatesMissingGenericSnapshot(t *testing.T) {
	d := setupOfflineDownload115DB(t)
	for range 2 {
		if err := MigrateOfflineDownload115Tasks(nil, "[]"); err != nil {
			t.Fatal(err)
		}
	}
	var snapshots []model.TaskItem
	if err := d.Find(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].Key != "download" || snapshots[0].PersistData != "[]" {
		t.Fatalf("generic snapshot not created idempotently: %+v", snapshots)
	}
}

func TestOfflineDownload115RejectsMissingIdentity(t *testing.T) {
	setupOfflineDownload115DB(t)
	for _, record := range []*model.OfflineDownload115Task{nil, {}} {
		if err := CreateOfflineDownload115Task(record); err == nil {
			t.Fatalf("create accepted missing identity: %+v", record)
		}
		if err := SaveOfflineDownload115Task(record); err == nil {
			t.Fatalf("save accepted missing identity: %+v", record)
		}
	}
	if err := MigrateOfflineDownload115Tasks([]model.OfflineDownload115Task{{}}, "[]"); err == nil {
		t.Fatal("migration accepted missing task ID")
	}
	if err := MarkOfflineDownload115TaskDeleted("", time.Now()); err == nil {
		t.Fatal("deletion accepted empty task ID")
	}
	if err := MarkOfflineDownload115TaskDeleted("task", time.Time{}); err == nil {
		t.Fatal("deletion accepted empty timestamp")
	}
}

func TestOfflineDownload115MySQLUsesBoundedPrimaryKey(t *testing.T) {
	conn, err := sql.Open("mysql", "user:pass@tcp(127.0.0.1:1)/db")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d, err := gorm.Open(mysql.New(mysql.Config{Conn: conn, SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true,
		NamingStrategy:       schema.NamingStrategy{TablePrefix: "ol_"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := schema.Parse(&model.OfflineDownload115Task{}, &sync.Map{}, d.NamingStrategy)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PrimaryFields) != 1 || s.PrimaryFields[0].Name != "TaskID" {
		t.Fatalf("primary key = %+v, want TaskID", s.PrimaryFields)
	}
	if got := strings.ToLower(d.Dialector.DataTypeOf(s.PrimaryFields[0])); got != "varchar(64)" {
		t.Fatalf("primary key column type=%q, want varchar(64)", got)
	}
	if got := strings.ToLower(d.Dialector.DataTypeOf(s.LookUpField("Snapshot"))); got != "text" {
		t.Fatalf("snapshot column type=%q, want text", got)
	}
	if !strings.HasPrefix(s.Table, "ol_") {
		t.Fatalf("table prefix was ignored: %q", s.Table)
	}
}
