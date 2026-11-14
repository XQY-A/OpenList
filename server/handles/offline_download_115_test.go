package handles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func setup115AddDownload(t *testing.T, downloadTool tool.Tool) (string, *tache.Manager[*tool.DownloadTask]) {
	t.Helper()
	addition, err := json.Marshal(map[string]string{"root_folder_path": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	mount := "/offline-add-" + uuid.NewString()
	storageID, err := op.CreateStorage(context.Background(), model.Storage{
		Driver: "Local", MountPath: mount, Addition: string(addition),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := op.DeleteStorageById(context.Background(), storageID); err != nil {
			t.Errorf("delete fixture storage: %v", err)
		}
	})
	previousTool, existed := tool.Tools[downloadTool.Name()]
	tool.Tools[downloadTool.Name()] = downloadTool
	t.Cleanup(func() {
		if existed {
			tool.Tools[downloadTool.Name()] = previousTool
		} else {
			delete(tool.Tools, downloadTool.Name())
		}
	})
	previousManager := tool.DownloadTaskManager
	manager := tache.NewManager[*tool.DownloadTask](tache.WithRunning(false), tache.WithWorks(0))
	tool.DownloadTaskManager = manager
	t.Cleanup(func() {
		for _, download := range manager.GetAll() {
			if err := db.GetDb().Where("task_id = ?", download.GetID()).Delete(&model.OfflineDownload115Task{}).Error; err != nil {
				t.Errorf("delete fixture task: %v", err)
			}
		}
		tool.DownloadTaskManager = previousManager
	})
	return mount, manager
}

func requestAddDownload(t *testing.T, user *model.User, req AddOfflineDownloadReq) common.Resp[json.RawMessage] {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	ctx, engine := gin.CreateTestContext(response)
	engine.ContextWithFallback = true
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/fs/add_offline_download", strings.NewReader(string(body)))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), conf.UserKey, user))
	AddOfflineDownload(ctx)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP code = %d, response = %s", response.Code, response.Body)
	}
	var result common.Resp[json.RawMessage]
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func fail115TaskCreation(t *testing.T, shouldFail func(string) bool, failure error) *int {
	t.Helper()
	name := "test:115-create-" + uuid.NewString()
	callbacks := db.GetDb().Callback().Create()
	attempts := new(int)
	if err := callbacks.Before("gorm:create").Register(name, func(tx *gorm.DB) {
		record, ok := tx.Statement.Dest.(*model.OfflineDownload115Task)
		if !ok {
			return
		}
		(*attempts)++
		var snapshot struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
			t.Errorf("decode created snapshot: %v", err)
			tx.AddError(err)
			return
		}
		if shouldFail(snapshot.URL) {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := callbacks.Remove(name); err != nil {
			t.Errorf("remove create callback: %v", err)
		}
	})
	return attempts
}

func Test115AddDownloadBatchResults(t *testing.T) {
	for _, test := range []struct {
		name     string
		urls     []string
		failURL  string
		code     int
		created  int
		attempts int
	}{
		{
			name: "all accepted locally", urls: []string{" magnet:?xt=urn:btih:first ", "magnet:?xt=urn:btih:second"},
			code: 200, created: 2, attempts: 2,
		},
		{
			name:    "partial success continues past failed insert",
			urls:    []string{"magnet:?xt=urn:btih:first", " \t ", "magnet:?xt=urn:btih:fail", "magnet:?xt=urn:btih:last"},
			failURL: "magnet:?xt=urn:btih:fail", code: 200, created: 2, attempts: 3,
		},
		{
			name: "all inserts fail", urls: []string{"magnet:?xt=urn:btih:fail", "magnet:?xt=urn:btih:fail"},
			failURL: "magnet:?xt=urn:btih:fail", code: 500, attempts: 2,
		},
		{name: "all blank", urls: []string{"", " \t\n"}, code: 400},
		{name: "empty array", urls: []string{}, code: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			mount, manager := setup115AddDownload(t, &settingsTool{name: tool.Open115Name})
			previousPrivacy := conf.PrivacyReg
			conf.PrivacyReg = []*regexp.Regexp{regexp.MustCompile(`access_token=[^ &]+`)}
			t.Cleanup(func() { conf.PrivacyReg = previousPrivacy })
			attempts := fail115TaskCreation(t, func(url string) bool { return url == test.failURL }, errors.New("insert failed: access_token=fixture-secret"))
			owner := &model.User{ID: 101, Username: "batch owner", Role: model.GENERAL, Permission: 1 << 2, BasePath: "/"}
			result := requestAddDownload(t, owner, AddOfflineDownloadReq{
				Urls: test.urls, Path: mount, Tool: tool.Open115Name, DeletePolicy: string(tool.DeleteNever),
			})
			if result.Code != test.code {
				t.Fatalf("response = %#v, want code %d", result, test.code)
			}
			if strings.Contains(string(result.Data), "fixture-secret") || strings.Contains(result.Message, "fixture-secret") {
				t.Fatal("private error details leaked into the response")
			}
			var data struct {
				Tasks   []map[string]json.RawMessage `json:"tasks"`
				Results []map[string]json.RawMessage `json:"results"`
			}
			if err := json.Unmarshal(result.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.Tasks == nil || data.Results == nil || len(data.Tasks) != test.created || len(data.Results) != len(test.urls) {
				t.Fatalf("unexpected batch data: %s", result.Data)
			}
			if *attempts != test.attempts || len(manager.GetAll()) != test.created {
				t.Fatalf("insert attempts = %d, manager tasks = %d; want %d and %d", *attempts, len(manager.GetAll()), test.attempts, test.created)
			}
			infos := make(map[string]map[string]json.RawMessage, len(data.Tasks))
			for _, info := range data.Tasks {
				assert115TaskInfo(t, info, tool.SubmissionPending, "")
				var id string
				if err := json.Unmarshal(info["id"], &id); err != nil {
					t.Fatal(err)
				}
				infos[id] = info
			}
			for index, item := range data.Results {
				var gotIndex int
				if err := json.Unmarshal(item["index"], &gotIndex); err != nil || gotIndex != index {
					t.Fatalf("result index = %s, want %d", item["index"], index)
				}
				trimmedURL := strings.TrimSpace(test.urls[index])
				_, failed := item["error"]
				if trimmedURL == "" || trimmedURL == test.failURL {
					if !failed || len(item["task_id"]) != 0 {
						t.Errorf("failed result = %v", item)
					}
					continue
				}
				var id string
				if err := json.Unmarshal(item["task_id"], &id); err != nil || id == "" || failed {
					t.Fatalf("successful result = %v", item)
				}
				download, exists := manager.GetByID(id)
				if !exists || download.Url != trimmedURL || download.GetCreator() == nil || download.GetCreator().ID != owner.ID || infos[id] == nil {
					t.Fatalf("result %d is not associated with the created task %s", index, id)
				}
				record, err := db.GetOfflineDownload115Task(id)
				if err != nil || record.SubmissionState != tool.SubmissionPending || record.InfoHash != "" {
					t.Fatalf("durable task = %#v, error = %v", record, err)
				}
			}
		})
	}
}

func Test115AddDownloadPermissions(t *testing.T) {
	mount, manager := setup115AddDownload(t, &settingsTool{name: tool.Open115Name})
	owner := &model.User{ID: 101, Permission: 1 << 2, BasePath: "/"}
	meta := &model.Meta{Path: mount, WriteUsers: []uint{owner.ID + 1}}
	if err := op.CreateMeta(meta); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := op.DeleteMetaById(meta.ID); err != nil {
			t.Errorf("delete fixture meta: %v", err)
		}
	})
	for _, test := range []struct {
		name string
		user *model.User
		code int
	}{
		{name: "invalid user", code: 401},
		{name: "no add permission", user: &model.User{ID: 102}, code: 403},
		{name: "no write permission", user: owner, code: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := requestAddDownload(t, test.user, AddOfflineDownloadReq{
				Urls: []string{"magnet:?xt=urn:btih:test"}, Path: mount, Tool: tool.Open115Name,
			})
			if result.Code != test.code || string(result.Data) != "null" || len(manager.GetAll()) != 0 {
				t.Fatalf("permission rejection = %#v, created tasks = %d", result, len(manager.GetAll()))
			}
		})
	}
}

type addDownloadFailingTool struct {
	settingsTool
}

func (*addDownloadFailingTool) IsReady() bool { return false }
func (t *addDownloadFailingTool) Init() (string, error) {
	t.initCalls++
	if t.initCalls == 2 {
		return "", errors.New("injected initialization failure")
	}
	return "test", nil
}

func TestOtherAddDownloadKeepsBatchBehavior(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "first error still stops batch"
		}
		t.Run(name, func(t *testing.T) {
			fake := &addDownloadFailingTool{settingsTool: settingsTool{name: "aria2"}}
			var downloadTool tool.Tool = &settingsTool{name: "aria2"}
			if fail {
				downloadTool = fake
			}
			mount, manager := setup115AddDownload(t, downloadTool)
			owner := &model.User{ID: 101, Permission: 1 << 2, BasePath: "/"}
			result := requestAddDownload(t, owner, AddOfflineDownloadReq{
				Urls: []string{"https://example.com/first", " ", "https://example.com/second", "https://example.com/third"},
				Path: mount, Tool: "aria2",
			})
			if fail {
				if result.Code != 500 || string(result.Data) != "null" || fake.initCalls != 2 || len(manager.GetAll()) != 1 {
					t.Fatalf("existing failure behavior changed: %#v, init calls %d, created %d", result, fake.initCalls, len(manager.GetAll()))
				}
				return
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(result.Data, &data); err != nil {
				t.Fatal(err)
			}
			if result.Code != 200 || len(data) != 1 || data["tasks"] == nil || len(manager.GetAll()) != 3 {
				t.Fatalf("existing success behavior changed: %#v, created %d", result, len(manager.GetAll()))
			}
		})
	}
}
