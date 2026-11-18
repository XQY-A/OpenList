package handles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
)

type cloudDeleteTestTool struct {
	settingsTool
	mu                sync.Mutex
	lookups           []tool.Open115Reference
	deletions         []tool.Open115Reference
	deleteSourceFiles []bool
	lookup            func(tool.Open115Reference) (*tool.Open115TaskStatus, error)
	delete            func(tool.Open115Reference) error
	validateErr       error
}

func (*cloudDeleteTestTool) Prepare(context.Context, string) (*tool.Open115Submission, error) {
	return nil, errors.New("unexpected cloud submission preparation")
}

func (f *cloudDeleteTestTool) ValidateReference(tool.Open115Reference) error {
	return f.validateErr
}

func (f *cloudDeleteTestTool) Lookup(_ context.Context, reference tool.Open115Reference) (*tool.Open115TaskStatus, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, reference)
	f.mu.Unlock()
	if f.lookup != nil {
		return f.lookup(reference)
	}
	return &tool.Open115TaskStatus{State: 2}, nil
}

func (f *cloudDeleteTestTool) Delete(_ context.Context, reference tool.Open115Reference, deleteSourceFile bool) error {
	f.mu.Lock()
	f.deletions = append(f.deletions, reference)
	f.deleteSourceFiles = append(f.deleteSourceFiles, deleteSourceFile)
	f.mu.Unlock()
	if f.delete != nil {
		return f.delete(reference)
	}
	return nil
}

func installCloudDeleteTestTool(t *testing.T) *cloudDeleteTestTool {
	t.Helper()
	fake := &cloudDeleteTestTool{settingsTool: settingsTool{name: tool.Open115Name}}
	previous, existed := tool.Tools[tool.Open115Name]
	tool.Tools[tool.Open115Name] = fake
	t.Cleanup(func() {
		if existed {
			tool.Tools[tool.Open115Name] = previous
		} else {
			delete(tool.Tools, tool.Open115Name)
		}
	})
	return fake
}

func restoreCloudDeleteFixtures(t *testing.T, fixtures ...task115Fixture) (*observed115TaskManager, []string) {
	t.Helper()
	_, ids := restore115TaskFixtures(t, fixtures...)
	for index, id := range ids {
		if fixtures[index].submission != tool.SubmissionAccepted {
			continue
		}
		record, err := db.GetOfflineDownload115Task(id)
		if err != nil {
			t.Fatal(err)
		}
		// Distinct receipts let mixed batches identify the exact cloud target.
		record.InfoHash = "Cloud-ReCeIpT:+/=" + id
		if err := db.SaveOfflineDownload115Task(record); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := tool.NewDownloadTaskManager(0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return &observed115TaskManager{Manager: manager}, ids
}

func cloudDeleteRequestPath(route, id, query string) string {
	path := "/" + route + "?" + query
	if route == "delete" {
		path += "&tid=" + id
	}
	return path
}

func cloudDeleteIDsBody(t *testing.T, ids ...string) string {
	t.Helper()
	data, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func decodeCloudDeleteResult(t *testing.T, response common.Resp[json.RawMessage]) taskDeleteResult {
	t.Helper()
	var result taskDeleteResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func decodeCloudDeleteBatch(t *testing.T, response common.Resp[json.RawMessage]) []taskDeleteResult {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("batch response = %#v", response)
	}
	var data struct {
		Results []taskDeleteResult `json:"results"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Results == nil {
		t.Fatal("batch results must be a JSON array")
	}
	return data.Results
}

func assertCloudDeletePersistence(t *testing.T, manager *observed115TaskManager, id string, deleted bool, cloudState string) {
	t.Helper()
	record, err := db.GetOfflineDownload115Task(id)
	if err != nil {
		t.Fatal(err)
	}
	_, exists := manager.GetByID(id)
	if exists == deleted || (record.DeletedAt != nil) != deleted || record.CloudDeleteState != cloudState {
		t.Errorf("task %s: present=%v, tombstone=%v, cloud state=%s; want deleted=%v, cloud state=%s",
			id, exists, record.DeletedAt, record.CloudDeleteState, deleted, cloudState)
	}
}

func Test115CloudDeleteOptionRejectsInvalidValues(t *testing.T) {
	for _, route := range []string{"delete", "delete_some", "clear_done", "clear_succeeded"} {
		t.Run(route, func(t *testing.T) {
			owner := new115TaskUser(t)
			manager, ids := restoreCloudDeleteFixtures(t, task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted})
			fake := installCloudDeleteTestTool(t)
			router := task115Router(manager)
			for _, query := range []string{
				"delete_cloud_record", "delete_cloud_record=", "delete_cloud_record=TRUE", "delete_cloud_record=1",
				"delete_cloud_record=yes", "delete_cloud_record=%20true",
				"delete_cloud_record=true&delete_cloud_record=true",
				"delete_cloud_record=false&delete_cloud_record=true",
				"delete_cloud_record=%zz", "delete_cloud_record=true&bad=%zz",
				"delete_source_file=true",
				"delete_cloud_record=false&delete_source_file=true",
				"delete_cloud_record=true&delete_source_file",
				"delete_cloud_record=true&delete_source_file=",
				"delete_cloud_record=true&delete_source_file=TRUE",
				"delete_cloud_record=true&delete_source_file=1",
				"delete_cloud_record=true&delete_source_file=%20true",
				"delete_cloud_record=true&delete_source_file=%zz",
				"delete_cloud_record=true&delete_source_file=true&delete_source_file=true",
				"delete_cloud_record=true&delete_source_file=false&delete_source_file=true",
			} {
				response := request115Task(t, router, owner, http.MethodPost,
					cloudDeleteRequestPath(route, ids[0], query), cloudDeleteIDsBody(t, ids[0]))
				if response.Code != 400 || string(response.Data) != "null" {
					t.Errorf("query %q response = %#v, want 400 without deletion results", query, response)
				}
				assertCloudDeletePersistence(t, manager, ids[0], false, "none")
			}
			if len(fake.lookups) != 0 || len(fake.deletions) != 0 || len(manager.removed) != 0 {
				t.Fatal("invalid deletion option triggered an action")
			}
			// Explicit false must preserve each route's pre-existing response.
			response := request115Task(t, router, owner, http.MethodPost,
				cloudDeleteRequestPath(route, ids[0], "delete_cloud_record=false"), cloudDeleteIDsBody(t, ids[0]))
			wantData := "null"
			if route == "delete_some" {
				wantData = "{}"
			}
			if response.Code != 200 || string(response.Data) != wantData || len(fake.lookups) != 0 || len(fake.deletions) != 0 {
				t.Fatalf("false changed the existing contract: %#v", response)
			}
			assertCloudDeletePersistence(t, manager, ids[0], true, "none")
		})
	}
}

func Test115SourceFileDeleteOptionsAcrossAllRoutes(t *testing.T) {
	for _, route := range []string{"delete", "delete_some", "clear_done", "clear_succeeded"} {
		for _, option := range []struct {
			name       string
			query      string
			cloud      bool
			deleteFile bool
		}{
			{name: "local_default"},
			{name: "local_explicit_false", query: "delete_cloud_record=false&delete_source_file=false"},
			{name: "cloud_default", query: "delete_cloud_record=true", cloud: true},
			{name: "cloud_explicit_false", query: "delete_cloud_record=true&delete_source_file=false", cloud: true},
			{name: "cloud_and_files", query: "delete_cloud_record=true&delete_source_file=true", cloud: true, deleteFile: true},
		} {
			t.Run(route+"/"+option.name, func(t *testing.T) {
				owner := new115TaskUser(t)
				manager, ids := restoreCloudDeleteFixtures(t, task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted})
				fake := installCloudDeleteTestTool(t)
				router := task115Router(manager)
				infoResponse := request115Task(t, router, owner, http.MethodPost, "/info?tid="+ids[0], "")
				var info TaskInfo
				if err := json.Unmarshal(infoResponse.Data, &info); err != nil || !info.CanDeleteSourceFile {
					t.Fatalf("source file capability was not advertised: %s, %v", infoResponse.Data, err)
				}
				response := request115Task(t, router, owner, http.MethodPost,
					cloudDeleteRequestPath(route, ids[0], option.query), cloudDeleteIDsBody(t, ids[0]))
				if response.Code != 200 {
					t.Fatalf("deletion response = %#v", response)
				}
				wantCloud, wantFiles := tool.CloudDeleteNone, tool.SourceFileDeleteNone
				if option.cloud {
					wantCloud = tool.CloudDeleteDeleted
					if option.deleteFile {
						wantFiles = tool.SourceFileDeleteDeleted
					}
					var result taskDeleteResult
					if route == "delete" {
						result = decodeCloudDeleteResult(t, response)
					} else {
						results := decodeCloudDeleteBatch(t, response)
						if len(results) != 1 {
							t.Fatalf("expected one result, got %+v", results)
						}
						result = results[0]
					}
					if !result.LocalDeleted || result.CloudState != wantCloud || result.SourceFileDeleteState != wantFiles {
						t.Fatalf("deletion result = %+v", result)
					}
					if len(fake.deleteSourceFiles) != 1 || fake.deleteSourceFiles[0] != option.deleteFile {
						t.Fatalf("cloud option = %v, want %v", fake.deleteSourceFiles, option.deleteFile)
					}
				} else if len(fake.lookups) != 0 || len(fake.deletions) != 0 {
					t.Fatal("local deletion contacted the cloud")
				}
				assertCloudDeletePersistence(t, manager, ids[0], true, wantCloud)
				record, err := db.GetOfflineDownload115Task(ids[0])
				if err != nil {
					t.Fatal(err)
				}
				var snapshot struct {
					DeleteSourceFile      bool   `json:"delete_source_file"`
					SourceFileDeleteState string `json:"source_file_delete_state"`
				}
				if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
					t.Fatal(err)
				}
				if snapshot.DeleteSourceFile != option.deleteFile ||
					(option.cloud && snapshot.SourceFileDeleteState != wantFiles) {
					t.Fatalf("saved file deletion option = %+v", snapshot)
				}
			})
		}
	}
}

func Test115CloudDeleteSingleResults(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      tache.State
		submission string
		configure  func(*testing.T, *cloudDeleteTestTool)
		code       int
		cloudState string
		deleted    bool
		lookups    int
		deletions  int
	}{
		{name: "completed", state: tache.StateSucceeded, code: 200, cloudState: "deleted", deleted: true, lookups: 1, deletions: 1},
		{name: "failed cloud task", state: tache.StateFailed, code: 200, cloudState: "deleted", deleted: true, lookups: 1, deletions: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.lookup = func(tool.Open115Reference) (*tool.Open115TaskStatus, error) {
					return &tool.Open115TaskStatus{State: -1}, nil
				}
			}},
		{name: "cloud record absent", state: tache.StateCanceled, code: 200, cloudState: "deleted", deleted: true, lookups: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.lookup = func(tool.Open115Reference) (*tool.Open115TaskStatus, error) { return nil, nil }
			}},
		{name: "cloud active", state: tache.StateSucceeded, code: 409, cloudState: "none", lookups: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.lookup = func(tool.Open115Reference) (*tool.Open115TaskStatus, error) {
					return &tool.Open115TaskStatus{State: 1}, nil
				}
			}},
		{name: "cloud state unrecognized", state: tache.StateSucceeded, code: 409, cloudState: "none", lookups: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.lookup = func(tool.Open115Reference) (*tool.Open115TaskStatus, error) {
					return &tool.Open115TaskStatus{State: 99}, nil
				}
			}},
		{name: "lookup failed", state: tache.StateFailed, code: 500, cloudState: "none", lookups: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.lookup = func(tool.Open115Reference) (*tool.Open115TaskStatus, error) { return nil, errors.New("lookup failed") }
			}},
		{name: "delete outcome unknown", state: tache.StateSucceeded, code: 500, cloudState: "unknown", lookups: 1, deletions: 1,
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.delete = func(tool.Open115Reference) error { return context.DeadlineExceeded }
			}},
		{name: "local task pending", state: tache.StatePending, code: 409, cloudState: "none"},
		{name: "receipt missing", state: tache.StateFailed, submission: tool.SubmissionUnknown, code: 409, cloudState: "none"},
		{name: "account changed", state: tache.StateSucceeded, code: 409, cloudState: "none",
			configure: func(_ *testing.T, fake *cloudDeleteTestTool) {
				fake.validateErr = fmt.Errorf("%w: account changed", tool.ErrTaskConflict)
			}},
		{name: "tombstone failed after cloud deletion", state: tache.StateSucceeded, code: 500, cloudState: "deleted", lookups: 1, deletions: 1,
			configure: func(t *testing.T, _ *cloudDeleteTestTool) { fail115TombstoneWrites(t) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := new115TaskUser(t)
			submission := test.submission
			if submission == "" {
				submission = tool.SubmissionAccepted
			}
			manager, ids := restoreCloudDeleteFixtures(t, task115Fixture{owner, test.state, submission})
			fake := installCloudDeleteTestTool(t)
			if test.configure != nil {
				test.configure(t, fake)
			}
			manager.beforeRemove = func(id string) {
				record, err := db.GetOfflineDownload115Task(id)
				if err != nil || record.DeletedAt == nil || record.CloudDeletedAt == nil || record.CloudDeleteState != "deleted" {
					t.Fatalf("manager removed task before cloud result and tombstone were durable: %#v, %v", record, err)
				}
			}
			response := request115Task(t, task115Router(manager), owner, http.MethodPost,
				cloudDeleteRequestPath("delete", ids[0], "delete_cloud_record=true"), "")
			result := decodeCloudDeleteResult(t, response)
			if response.Code != test.code || result.Code != test.code || result.TaskID != ids[0] || result.LocalDeleted != test.deleted || result.CloudState != test.cloudState {
				t.Fatalf("response = %#v, data = %#v", response, result)
			}
			if (result.Error == "") != (test.code == 200) || (test.code != 200 && result.Reason == "") {
				t.Errorf("unexpected error detail: %#v", result)
			}
			if len(fake.lookups) != test.lookups || len(fake.deletions) != test.deletions {
				t.Errorf("lookups=%d, deletes=%d; want %d and %d", len(fake.lookups), len(fake.deletions), test.lookups, test.deletions)
			}
			for _, reference := range append(fake.lookups, fake.deletions...) {
				if reference.StorageID != 1 || reference.AccountID != "12345" || reference.InfoHash != "Cloud-ReCeIpT:+/="+ids[0] {
					t.Errorf("cloud reference changed: %#v", reference)
				}
			}
			assertCloudDeletePersistence(t, manager, ids[0], test.deleted, test.cloudState)
		})
	}
}

func Test115CloudDeleteBatchPartialSuccessAndDuplicates(t *testing.T) {
	owner := new115TaskUser(t)
	other := new115TaskUser(t)
	manager, ids := restoreCloudDeleteFixtures(t,
		task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted},
		task115Fixture{owner, tache.StateFailed, tool.SubmissionAccepted},
		task115Fixture{owner, tache.StatePending, tool.SubmissionAccepted},
		task115Fixture{other, tache.StateSucceeded, tool.SubmissionAccepted})
	fake := installCloudDeleteTestTool(t)
	fake.delete = func(reference tool.Open115Reference) error {
		if reference.InfoHash == "Cloud-ReCeIpT:+/="+ids[1] {
			return errors.New("cloud request failed: access_token=private-value")
		}
		return nil
	}
	previousPrivacy := conf.PrivacyReg
	conf.PrivacyReg = []*regexp.Regexp{regexp.MustCompile(`access_token=[^ &]+`)}
	t.Cleanup(func() { conf.PrivacyReg = previousPrivacy })
	ordinary := &tool.DownloadTask{TaskExtension: task.TaskExtension{Creator: owner}, Toolname: "115 Cloud"}
	ordinary.SetState(tache.StateSucceeded)
	manager.Add(ordinary)
	requested := []string{ids[0], ids[1], ids[2], ids[3], "missing-task", ordinary.GetID(), ids[0], ids[1]}
	response := request115Task(t, task115Router(manager), owner, http.MethodPost,
		"/delete_some?delete_cloud_record=true&delete_source_file=true", cloudDeleteIDsBody(t, requested...))
	results := decodeCloudDeleteBatch(t, response)
	if len(results) != len(requested) || strings.Contains(string(response.Data), "private-value") || strings.Contains(string(response.Data), "Cloud-ReCeIpT") {
		t.Fatalf("unexpected batch results: %s", response.Data)
	}
	wantCodes := []int{200, 500, 409, 404, 404, 409, 200, 500}
	wantFiles := []string{"deleted", "unknown", "none", "none", "none", "none", "deleted", "unknown"}
	for index, result := range results {
		if result.TaskID != requested[index] || result.Code != wantCodes[index] ||
			result.LocalDeleted != (wantCodes[index] == 200) || result.SourceFileDeleteState != wantFiles[index] {
			t.Errorf("result %d = %#v, want code %d", index, result, wantCodes[index])
		}
	}
	if !reflect.DeepEqual(results[0], results[6]) || !reflect.DeepEqual(results[1], results[7]) || len(fake.lookups) != 2 || len(fake.deletions) != 2 {
		t.Errorf("duplicate IDs repeated an operation: results=%#v, lookups=%d, deletions=%d", results, len(fake.lookups), len(fake.deletions))
	}
	if !reflect.DeepEqual(fake.deleteSourceFiles, []bool{true, true}) {
		t.Fatalf("mixed batch lost file deletion options: %v", fake.deleteSourceFiles)
	}
	if results[3].Reason != "task_not_found" || results[4].Reason != "task_not_found" || results[5].Reason != "unsupported_tool" {
		t.Errorf("unexpected failure categories: %#v", results)
	}
	assertCloudDeletePersistence(t, manager, ids[0], true, "deleted")
	assertCloudDeletePersistence(t, manager, ids[1], false, "unknown")
	assertCloudDeletePersistence(t, manager, ids[2], false, "none")
	assertCloudDeletePersistence(t, manager, ids[3], false, "none")
	if _, exists := manager.GetByID(ordinary.GetID()); !exists {
		t.Fatal("unsupported task silently fell back to local removal")
	}
}

func Test115CloudDeleteClearFiltersAndRetainsFailures(t *testing.T) {
	for _, route := range []string{"clear_done", "clear_succeeded"} {
		t.Run(route, func(t *testing.T) {
			owner := new115TaskUser(t)
			other := new115TaskUser(t)
			manager, ids := restoreCloudDeleteFixtures(t,
				task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted},
				task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted},
				task115Fixture{owner, tache.StateFailed, tool.SubmissionAccepted},
				task115Fixture{owner, tache.StateCanceled, tool.SubmissionAccepted},
				task115Fixture{owner, tache.StatePending, tool.SubmissionAccepted},
				task115Fixture{other, tache.StateSucceeded, tool.SubmissionAccepted},
				task115Fixture{nil, tache.StateSucceeded, tool.SubmissionAccepted})
			fake := installCloudDeleteTestTool(t)
			fake.lookup = func(reference tool.Open115Reference) (*tool.Open115TaskStatus, error) {
				if reference.InfoHash == "Cloud-ReCeIpT:+/="+ids[1] {
					return nil, errors.New("lookup unavailable")
				}
				return &tool.Open115TaskStatus{State: 2}, nil
			}
			response := request115Task(t, task115Router(manager), owner, http.MethodPost,
				"/"+route+"?delete_cloud_record=true&delete_source_file=true", "")
			results := decodeCloudDeleteBatch(t, response)
			wantCount := 2
			if route == "clear_done" {
				wantCount = 4
			}
			if len(results) != wantCount || len(fake.lookups) != wantCount || len(fake.deletions) != wantCount-1 {
				t.Fatalf("clear results=%#v, lookups=%d, deletions=%d", results, len(fake.lookups), len(fake.deletions))
			}
			byID := make(map[string]taskDeleteResult, len(results))
			for _, result := range results {
				byID[result.TaskID] = result
			}
			for index, id := range ids {
				wantDeleted := index < wantCount && index != 1
				cloudState := "none"
				if wantDeleted {
					cloudState = "deleted"
				}
				assertCloudDeletePersistence(t, manager, id, wantDeleted, cloudState)
				result, exists := byID[id]
				if exists != (index < wantCount) {
					t.Errorf("task %d improperly included or excluded from clear: %#v", index, results)
				}
				if exists && (result.LocalDeleted != wantDeleted || (result.Code == 200) != wantDeleted) {
					t.Errorf("clear result does not describe the retained task: %#v", result)
				}
				if exists {
					wantFiles := tool.SourceFileDeleteNone
					if wantDeleted {
						wantFiles = tool.SourceFileDeleteDeleted
					}
					if result.SourceFileDeleteState != wantFiles {
						t.Errorf("clear result lost the file deletion outcome: %+v", result)
					}
				}
			}
			for _, deleteFiles := range fake.deleteSourceFiles {
				if !deleteFiles {
					t.Fatal("clear dropped the explicit file deletion option")
				}
			}
		})
	}
}

func Test115CloudDeletePermissionsAndOrphanedCreator(t *testing.T) {
	owner := new115TaskUser(t)
	other := new115TaskUser(t)
	manager, ids := restoreCloudDeleteFixtures(t,
		task115Fixture{owner, tache.StateSucceeded, tool.SubmissionAccepted},
		task115Fixture{nil, tache.StateSucceeded, tool.SubmissionAccepted})
	fake := installCloudDeleteTestTool(t)
	router := task115Router(manager)
	var previous taskDeleteResult
	for index, id := range []string{ids[0], ids[1], "nonexistent"} {
		response := request115Task(t, router, other, http.MethodPost,
			cloudDeleteRequestPath("delete", id, "delete_cloud_record=true"), "")
		result := decodeCloudDeleteResult(t, response)
		if response.Code != 404 || result.Code != 404 || result.LocalDeleted || result.CloudState != "none" || result.Reason != "task_not_found" || strings.Contains(string(response.Data), "Cloud-ReCeIpT") {
			t.Fatalf("unauthorized deletion response = %#v, %#v", response, result)
		}
		result.TaskID = ""
		if index != 0 && !reflect.DeepEqual(previous, result) {
			t.Errorf("not-found and unauthorized responses differ: %#v, %#v", previous, result)
		}
		previous = result
	}
	for _, route := range []string{"delete", "delete_some", "clear_done", "clear_succeeded"} {
		response := request115Task(t, router, nil, http.MethodPost,
			cloudDeleteRequestPath(route, ids[0], "delete_cloud_record=true"), cloudDeleteIDsBody(t, ids[0]))
		if response.Code != 401 {
			t.Errorf("invalid user response for %s = %#v", route, response)
		}
	}
	if len(fake.lookups) != 0 || len(fake.deletions) != 0 {
		t.Fatal("unauthorized user accessed cloud task information")
	}
	admin := &model.User{Role: model.ADMIN}
	response := request115Task(t, router, admin, http.MethodPost, "/clear_succeeded?delete_cloud_record=true", "")
	results := decodeCloudDeleteBatch(t, response)
	if len(results) != 2 || len(fake.deletions) != 2 {
		t.Fatalf("administrator could not delete an orphaned task: %#v", results)
	}
	for _, result := range results {
		if result.Code != 200 || !result.LocalDeleted {
			t.Errorf("administrator deletion failed: %#v", result)
		}
		assertCloudDeletePersistence(t, manager, result.TaskID, true, "deleted")
	}
}

func Test115CloudDeleteUnsupportedTransferAndEmptyBatch(t *testing.T) {
	owner := new115TaskUser(t)
	manager := tache.NewManager[*tool.TransferTask](tache.WithRunning(false), tache.WithWorks(0))
	transfer := &tool.TransferTask{TaskData: fs.TaskData{TaskExtension: task.TaskExtension{Creator: owner}}}
	transfer.SetState(tache.StateSucceeded)
	manager.Add(transfer)
	router := gin.New()
	taskRoute(router.Group("/offline_download"), manager)
	for _, route := range []string{"delete", "delete_some", "clear_done", "clear_succeeded"} {
		response := request115Task(t, router, owner, http.MethodPost,
			cloudDeleteRequestPath(route, transfer.GetID(), "delete_cloud_record=true"), cloudDeleteIDsBody(t, transfer.GetID()))
		var result taskDeleteResult
		if route == "delete" {
			result = decodeCloudDeleteResult(t, response)
			if response.Code != 409 {
				t.Fatalf("unsupported single delete response = %#v", response)
			}
		} else {
			results := decodeCloudDeleteBatch(t, response)
			if len(results) != 1 {
				t.Fatalf("unsupported batch results = %#v", results)
			}
			result = results[0]
		}
		if result.Code != 409 || result.LocalDeleted || result.CloudState != "none" || result.Reason != "unsupported_tool" {
			t.Errorf("unsupported transfer result = %#v", result)
		}
		if _, exists := manager.GetByID(transfer.GetID()); !exists {
			t.Fatal("cloud deletion removed an unsupported transfer task")
		}
	}
	response := request115Task(t, router, owner, http.MethodPost, "/delete_some?delete_cloud_record=true", "[]")
	if results := decodeCloudDeleteBatch(t, response); len(results) != 0 {
		t.Fatalf("empty batch results = %#v", results)
	}
}
