package handles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	_115_open "github.com/OpenListTeam/OpenList/v4/drivers/115_open"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Only the cloud boundary is simulated. The handler, task queue, worker,
// durable store, recovery, and deletion service all use their real code paths.
type lifecycle115Cloud struct {
	settingsTool
	mu            sync.Mutex
	reference     tool.Open115Reference
	mount         string
	prepared      []string
	submitted     []string
	lookups       []tool.Open115Reference
	deleted       []tool.Open115Reference
	records       map[string]bool
	files         map[string]string
	lookupStarted chan struct{}
	complete      chan struct{}
	completeOnce  sync.Once
}

func (c *lifecycle115Cloud) Prepare(ctx context.Context, path string) (*tool.Open115Submission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	storage, actualPath, err := op.GetStorageAndActualPath(path)
	if err != nil {
		return nil, err
	}
	if path != c.mount || actualPath != "/" || storage.GetStorage().ID != c.reference.StorageID {
		return nil, errors.New("unexpected 115 preparation path or storage")
	}
	c.mu.Lock()
	c.prepared = append(c.prepared, path)
	c.mu.Unlock()
	reference := c.reference
	reference.InfoHash = ""
	return &tool.Open115Submission{
		Reference: reference,
		Submit: func(ctx context.Context, url string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			c.submitted = append(c.submitted, url)
			c.records[c.reference.InfoHash] = true
			return c.reference.InfoHash, nil
		},
	}, nil
}

func (c *lifecycle115Cloud) ValidateReference(reference tool.Open115Reference) error {
	if reference != c.reference {
		return fmt.Errorf("unexpected cloud reference: %+v", reference)
	}
	return nil
}

func (c *lifecycle115Cloud) Lookup(ctx context.Context, reference tool.Open115Reference) (*tool.Open115TaskStatus, error) {
	if err := c.ValidateReference(reference); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.lookups = append(c.lookups, reference)
	c.mu.Unlock()
	select {
	case c.lookupStarted <- struct{}{}:
	default:
	}
	select {
	case <-c.complete:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.records[reference.InfoHash] {
		return nil, nil
	}
	c.files[reference.InfoHash] = "completed fixture file"
	return &tool.Open115TaskStatus{
		State: 2,
		Status: tool.Status{
			Completed: true, Progress: 100, TotalBytes: 22, Status: "download complete",
		},
	}, nil
}

func (c *lifecycle115Cloud) Delete(ctx context.Context, reference tool.Open115Reference, deleteSourceFile bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.ValidateReference(reference); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, reference)
	delete(c.records, reference.InfoHash)
	if deleteSourceFile {
		delete(c.files, reference.InfoHash)
	}
	return nil
}

func (c *lifecycle115Cloud) finish() { c.completeOnce.Do(func() { close(c.complete) }) }

func (c *lifecycle115Cloud) assertLedger(t *testing.T, submissions, lookups, deletions int, record, file bool) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.prepared) != submissions || len(c.submitted) != submissions || len(c.lookups) != lookups || len(c.deleted) != deletions {
		t.Fatalf("cloud calls: prepare=%d submit=%d lookup=%d delete=%d; want %d/%d/%d/%d",
			len(c.prepared), len(c.submitted), len(c.lookups), len(c.deleted), submissions, submissions, lookups, deletions)
	}
	if c.records[c.reference.InfoHash] != record || (c.files[c.reference.InfoHash] == "completed fixture file") != file {
		t.Fatalf("cloud ledgers: records=%v files=%v; want record=%v file=%v", c.records, c.files, record, file)
	}
}

type lifecycle115Barrier struct {
	settingsTool
	started chan struct{}
}

func (b *lifecycle115Barrier) Run(*tool.DownloadTask) error {
	b.started <- struct{}{}
	return errors.New("worker barrier finished")
}

// With one worker, starting this ordinary, nonpersistent task proves that the
// preceding download has finished its worker's terminal state/error writes.
// tache.Manager.Wait reads its worker counter without synchronization, so it
// cannot be used in a race-enabled test. The barrier's own tail touches only
// its private task and the manager, never the database or global tool map.
func drain115LifecycleManager(t *testing.T, manager *tache.Manager[*tool.DownloadTask], barrier *lifecycle115Barrier) bool {
	t.Helper()
	queued := &tool.DownloadTask{Toolname: barrier.Name()}
	manager.Add(queued)
	manager.Start()
	select {
	case <-barrier.started:
		manager.Pause()
		manager.Remove(queued.GetID())
		return true
	case <-time.After(5 * time.Second):
		manager.Pause()
		t.Error("download worker did not reach the completion barrier")
		return false
	}
}

func assert115LifecycleInfo(t *testing.T, router *gin.Engine, user *model.User, id, submission, hash string, state tache.State) {
	t.Helper()
	response := request115Task(t, router, user, http.MethodPost, "/info?tid="+id, "")
	if response.Code != 200 {
		t.Fatalf("task info response = %#v", response)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Data, &fields); err != nil {
		t.Fatal(err)
	}
	assert115TaskInfo(t, fields, submission, hash)
	var info TaskInfo
	if err := json.Unmarshal(response.Data, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != id || info.State != state || info.Creator == "" || info.Error != "" {
		t.Fatalf("unexpected task info: %s", response.Data)
	}
	if state == tache.StateSucceeded && (info.EndTime == nil || info.StartTime == nil || info.Progress != 100 || !info.CanDeleteCloudRecord || !info.CanDeleteSourceFile) {
		t.Fatalf("completed task metadata is incomplete: %s", response.Data)
	}
}

func assert115LifecycleLists(t *testing.T, router *gin.Engine, owner, admin, other *model.User, id, hash string) {
	t.Helper()
	for _, user := range []*model.User{owner, admin, other} {
		for _, route := range []string{"done", "undone"} {
			response := request115Task(t, router, user, http.MethodGet, "/"+route, "")
			if response.Code != 200 {
				t.Fatalf("%s response = %#v", route, response)
			}
			var infos []map[string]json.RawMessage
			if err := json.Unmarshal(response.Data, &infos); err != nil {
				t.Fatal(err)
			}
			want := 0
			if id != "" && route == "done" && user != other {
				want = 1
			}
			if infos == nil || len(infos) != want {
				t.Fatalf("%s list for %s = %s, want %d entries", route, user.Username, response.Data, want)
			}
			if want != 0 {
				assert115TaskInfo(t, infos[0], tool.SubmissionAccepted, hash)
				if string(infos[0]["id"]) != `"`+id+`"` {
					t.Fatalf("list returned another task: %s", response.Data)
				}
			}
		}
	}
}

func Test115OfflineLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deleteCloud bool
		deleteFiles bool
	}{
		{name: "delete_cloud_record", deleteCloud: true},
		{name: "delete_cloud_record_and_files", deleteCloud: true, deleteFiles: true},
		{name: "default_local_delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := new115TaskUser(t)
			owner.BasePath, owner.Permission = "/", 1<<2
			if err := db.UpdateUser(owner); err != nil {
				t.Fatal(err)
			}
			other := new115TaskUser(t)
			admin := &model.User{Role: model.ADMIN, Username: "lifecycle administrator"}

			// No fixture tasks are synthesized: this helper only isolates the
			// legacy snapshot and calls the real recovery constructor, paused.
			initial, _ := restore115TaskFixtures(t)
			if len(initial.GetAll()) != 0 {
				t.Fatal("lifecycle fixture unexpectedly restored unrelated tasks")
			}
			initial.SetWorkersNumActive(1)
			mount := "/offline-lifecycle-" + uuid.NewString()
			// Empty credentials fail before the driver creates an HTTP client.
			// op retains the real driver, whose root is resolved locally. Driver
			// authentication is outside this Open115Tool integration boundary.
			storageID, err := op.CreateStorage(context.Background(), model.Storage{
				Driver: tool.Open115Name, MountPath: mount, Addition: `{"root_folder_id":"0"}`,
			})
			if storageID != 0 {
				t.Cleanup(func() {
					if err := op.DeleteStorageById(context.Background(), storageID); err != nil {
						t.Errorf("delete lifecycle storage: %v", err)
					}
				})
			}
			if err == nil || !strings.Contains(err.Error(), "access token or refresh token is required") {
				t.Fatalf("expected credential-free initialization failure, got ID=%d error=%v", storageID, err)
			}
			storage, err := op.GetStorageByMountPath(mount)
			if err != nil {
				t.Fatal(err)
			}
			if open, ok := storage.(*_115_open.Open115); !ok || open.AccountID() != "" {
				t.Fatal("fixture must use an unauthenticated real 115 Open storage")
			}
			cloud := &lifecycle115Cloud{
				settingsTool: settingsTool{name: tool.Open115Name}, mount: mount,
				reference: tool.Open115Reference{StorageID: storageID, AccountID: "99115001", InfoHash: "MiXeD-115-HaSh:+/="},
				records:   make(map[string]bool), files: make(map[string]string),
				lookupStarted: make(chan struct{}, 1), complete: make(chan struct{}),
			}
			barrier := &lifecycle115Barrier{
				settingsTool: settingsTool{name: "lifecycle-barrier-" + uuid.NewString()}, started: make(chan struct{}, 1),
			}
			previousTool, hadTool := tool.Tools[tool.Open115Name]
			previousManager := tool.DownloadTaskManager
			tool.Tools[tool.Open115Name], tool.Tools[barrier.Name()] = cloud, barrier
			tool.DownloadTaskManager = initial.Manager
			managers := []*tache.Manager[*tool.DownloadTask]{initial.Manager}
			var taskID string
			t.Cleanup(func() {
				cloud.finish()
				for _, manager := range managers {
					drain115LifecycleManager(t, manager, barrier)
				}
				// The initial manager retains every task created by the add
				// handler, including when an assertion fails before decoding
				// its response or after a recovered manager removes the task.
				for _, download := range initial.GetAll() {
					if download.Is115Open() {
						if err := db.GetDb().Where("task_id = ?", download.GetID()).Delete(&model.OfflineDownload115Task{}).Error; err != nil {
							t.Errorf("delete lifecycle receipt/tombstone: %v", err)
						}
					}
				}
				tool.DownloadTaskManager = previousManager
				if hadTool {
					tool.Tools[tool.Open115Name] = previousTool
				} else {
					delete(tool.Tools, tool.Open115Name)
				}
				delete(tool.Tools, barrier.Name())
			})

			url := "magnet:?xt=urn:btih:original-input"
			added := requestAddDownload(t, owner, AddOfflineDownloadReq{
				Urls: []string{url}, Path: mount, Tool: tool.Open115Name, DeletePolicy: string(tool.DeleteNever),
			})
			if added.Code != 200 {
				t.Fatalf("add response = %#v", added)
			}
			var data struct {
				Tasks   []map[string]json.RawMessage `json:"tasks"`
				Results []struct {
					Index  int    `json:"index"`
					TaskID string `json:"task_id"`
					Error  string `json:"error"`
				} `json:"results"`
			}
			if err := json.Unmarshal(added.Data, &data); err != nil {
				t.Fatal(err)
			}
			if len(data.Tasks) != 1 || len(data.Results) != 1 || data.Results[0].TaskID == "" || data.Results[0].Index != 0 || data.Results[0].Error != "" {
				t.Fatalf("unexpected add data: %s", added.Data)
			}
			taskID = data.Results[0].TaskID
			assert115TaskInfo(t, data.Tasks[0], tool.SubmissionPending, "")
			download, ok := initial.GetByID(taskID)
			if !ok || download.TempDir != mount || download.DstDirPath != mount {
				t.Fatal("AddURL did not select the direct 115 destination")
			}
			router := task115Router(initial)
			assert115LifecycleInfo(t, router, owner, taskID, tool.SubmissionPending, "", tache.StatePending)
			stored, err := db.GetOfflineDownload115Task(taskID)
			if err != nil || stored.SubmissionState != tool.SubmissionPending || stored.InfoHash != "" || stored.CreatorID != owner.ID {
				t.Fatalf("initial durable receipt = %+v, error = %v", stored, err)
			}
			cloud.assertLedger(t, 0, 0, 0, false, false)

			initial.Start()
			select {
			case <-cloud.lookupStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("real worker did not submit and start cloud polling")
			}
			hash := cloud.reference.InfoHash
			assert115LifecycleInfo(t, router, owner, taskID, tool.SubmissionAccepted, hash, tache.StateRunning)
			stored, err = db.GetOfflineDownload115Task(taskID)
			if err != nil || stored.InfoHash != hash || stored.StorageID != storageID || stored.AccountID != cloud.reference.AccountID || stored.AcceptedAt == nil {
				t.Fatalf("accepted durable receipt = %+v, error = %v", stored, err)
			}
			cloud.assertLedger(t, 1, 1, 0, true, false)
			cloud.mu.Lock()
			submittedURL := cloud.submitted[0]
			cloud.mu.Unlock()
			if submittedURL != url {
				t.Fatalf("submitted URL = %q, want %q", submittedURL, url)
			}
			cloud.finish()
			if !drain115LifecycleManager(t, initial.Manager, barrier) {
				t.FailNow()
			}
			for _, user := range []*model.User{owner, admin} {
				assert115LifecycleInfo(t, router, user, taskID, tool.SubmissionAccepted, hash, tache.StateSucceeded)
			}
			assert115LifecycleLists(t, router, owner, admin, other, taskID, hash)
			denied := request115Task(t, router, other, http.MethodPost, "/info?tid="+taskID, "")
			if denied.Code != 404 || string(denied.Data) != "null" || strings.Contains(denied.Message, hash) {
				t.Fatalf("another user received task metadata: %#v", denied)
			}

			restore := func() *observed115TaskManager {
				manager, err := tool.NewDownloadTaskManager(1, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				managers = append(managers, manager)
				tool.DownloadTaskManager = manager
				if !drain115LifecycleManager(t, manager, barrier) {
					t.FailNow()
				}
				return &observed115TaskManager{Manager: manager}
			}
			restored := restore()
			recovered, ok := restored.GetByID(taskID)
			if !ok || recovered == download || recovered.GetCreator() == nil || recovered.GetCreator().ID != owner.ID {
				t.Fatal("recovery did not rebuild the task and creator from persistence")
			}
			router = task115Router(restored)
			for _, user := range []*model.User{owner, admin} {
				assert115LifecycleInfo(t, router, user, taskID, tool.SubmissionAccepted, hash, tache.StateSucceeded)
			}
			assert115LifecycleLists(t, router, owner, admin, other, taskID, hash)
			cloud.assertLedger(t, 1, 1, 0, true, true)

			path := "/delete?tid=" + taskID
			if tc.deleteCloud {
				path += "&delete_cloud_record=true"
			}
			if tc.deleteFiles {
				path += "&delete_source_file=true"
			}
			denied = request115Task(t, router, other, http.MethodPost, path, "")
			if denied.Code != 404 || strings.Contains(string(denied.Data), hash) {
				t.Fatalf("another user could delete or inspect the task: %#v", denied)
			}
			cloud.assertLedger(t, 1, 1, 0, true, true)
			removed := request115Task(t, router, owner, http.MethodPost, path, "")
			if removed.Code != 200 {
				t.Fatalf("delete response = %#v", removed)
			}
			wantCloudState, wantLookups, wantDeletes := tool.CloudDeleteNone, 1, 0
			wantFileState := tool.SourceFileDeleteNone
			if tc.deleteFiles {
				wantFileState = tool.SourceFileDeleteDeleted
			}
			if tc.deleteCloud {
				wantCloudState, wantLookups, wantDeletes = tool.CloudDeleteDeleted, 2, 1
				result := decodeCloudDeleteResult(t, removed)
				if result.TaskID != taskID || !result.LocalDeleted || result.CloudState != wantCloudState || result.SourceFileDeleteState != wantFileState || result.Code != 200 {
					t.Fatalf("cloud deletion result = %+v", result)
				}
			} else if string(removed.Data) != "null" {
				t.Fatalf("default deletion changed the legacy response: %s", removed.Data)
			}
			stored, err = db.GetOfflineDownload115Task(taskID)
			if err != nil || stored.DeletedAt == nil || stored.CloudDeleteState != wantCloudState || stored.InfoHash != hash {
				t.Fatalf("durable tombstone = %+v, error = %v", stored, err)
			}
			var snapshot struct {
				DeleteSourceFile      bool   `json:"delete_source_file"`
				SourceFileDeleteState string `json:"source_file_delete_state"`
			}
			if err := json.Unmarshal([]byte(stored.Snapshot), &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.DeleteSourceFile != tc.deleteFiles ||
				(tc.deleteCloud && snapshot.SourceFileDeleteState != wantFileState) {
				t.Fatalf("durable file deletion result = %+v", snapshot)
			}
			if _, exists := restored.GetByID(taskID); exists {
				t.Fatal("successful deletion left the task in its manager")
			}
			cloud.assertLedger(t, 1, wantLookups, wantDeletes, !tc.deleteCloud, !tc.deleteFiles)
			afterDelete := restore()
			if len(afterDelete.GetAll()) != 0 {
				t.Fatal("a deleted task reappeared after recovery")
			}
			router = task115Router(afterDelete)
			assert115LifecycleLists(t, router, owner, admin, other, "", "")
			for _, user := range []*model.User{owner, admin} {
				response := request115Task(t, router, user, http.MethodPost, "/info?tid="+taskID, "")
				if response.Code != 404 || string(response.Data) != "null" {
					t.Fatalf("deleted task is still queryable: %#v", response)
				}
			}
			cloud.assertLedger(t, 1, wantLookups, wantDeletes, !tc.deleteCloud, !tc.deleteFiles)
		})
	}
}
