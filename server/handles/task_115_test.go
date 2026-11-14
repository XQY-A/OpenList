package handles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type observed115TaskManager struct {
	*tache.Manager[*tool.DownloadTask]
	beforeRemove func(string)
	removed      []string
	retried      []string
}

func (m *observed115TaskManager) Remove(id string) {
	if m.beforeRemove != nil {
		m.beforeRemove(id)
	}
	m.removed = append(m.removed, id)
	m.Manager.Remove(id)
}

func (m *observed115TaskManager) Retry(id string) {
	m.retried = append(m.retried, id)
	m.Manager.Retry(id)
}

type task115Fixture struct {
	creator    *model.User
	state      tache.State
	submission string
}

func new115TaskUser(t *testing.T) *model.User {
	t.Helper()
	user := &model.User{Username: "task-115-" + uuid.NewString(), Role: model.GENERAL}
	if err := db.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.DeleteUserById(user.ID); err != nil {
			t.Errorf("delete fixture user: %v", err)
		}
	})
	return user
}

func restore115TaskFixtures(t *testing.T, fixtures ...task115Fixture) (*observed115TaskManager, []string) {
	t.Helper()
	database := db.GetDb()
	var previous []model.TaskItem
	if err := database.Where("key = ?", "download").Find(&previous).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Where("key = ?", "download").Delete(&model.TaskItem{}).Error; err != nil {
			t.Errorf("delete fixture snapshot: %v", err)
		}
		if len(previous) != 0 {
			if err := database.Create(&previous).Error; err != nil {
				t.Errorf("restore previous snapshot: %v", err)
			}
		}
	})
	if err := database.Where("key = ?", "download").Delete(&model.TaskItem{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.TaskItem{Key: "download", PersistData: "[]"}).Error; err != nil {
		t.Fatal(err)
	}

	ids := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		snapshot, err := json.Marshal(map[string]any{
			"url":           "magnet:?xt=urn:btih:fixture",
			"dst_dir_path":  "/115/Downloads",
			"temp_dir":      "/115/Downloads",
			"delete_policy": "delete_never",
			"state":         fixture.state,
			"status":        "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		record := model.OfflineDownload115Task{
			TaskID: uuid.NewString(), Version: 1, StorageID: 1, AccountID: "12345",
			SubmissionState: fixture.submission, CloudDeleteState: "none",
			CreatedAt: now, UpdatedAt: now, Snapshot: string(snapshot),
		}
		if fixture.creator != nil {
			record.CreatorID = fixture.creator.ID
		}
		if fixture.submission == tool.SubmissionAccepted {
			record.InfoHash = "fixture-info-hash"
			record.AcceptedAt = &now
		}
		if err := db.CreateOfflineDownload115Task(&record); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, record.TaskID)
		t.Cleanup(func() {
			if err := database.Where("task_id = ?", record.TaskID).Delete(&model.OfflineDownload115Task{}).Error; err != nil {
				t.Errorf("delete fixture task: %v", err)
			}
		})
	}
	// Leave workers paused. Handler tests must never submit or poll cloud tasks.
	manager, err := tool.NewDownloadTaskManager(0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return &observed115TaskManager{Manager: manager}, ids
}

func task115Router(manager *observed115TaskManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	taskRoute[*tool.DownloadTask](router.Group("/offline_download"), manager)
	return router
}

func request115Task(t *testing.T, router *gin.Engine, user *model.User, method, path, body string) common.Resp[json.RawMessage] {
	t.Helper()
	request := httptest.NewRequest(method, "/offline_download"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), conf.UserKey, user))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, response = %s", response.Code, response.Body.String())
	}
	var result common.Resp[json.RawMessage]
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func fail115TombstoneWrites(t *testing.T) {
	t.Helper()
	name := "test:115-tombstone-" + uuid.NewString()
	callbacks := db.GetDb().Callback().Update()
	if err := callbacks.Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*model.OfflineDownload115Task); !ok {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]interface{})
		if !ok {
			return
		}
		if _, deleting := updates["deleted_at"]; deleting {
			tx.AddError(errors.New("injected tombstone write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := callbacks.Remove(name); err != nil {
			t.Errorf("remove failure callback: %v", err)
		}
	})
}

func Test115TaskDeletionPersistsBeforeRemoval(t *testing.T) {
	for _, route := range []string{"delete", "delete_some", "clear_done", "clear_succeeded"} {
		for _, failWrite := range []bool{false, true} {
			name := route + "/success"
			if failWrite {
				name = route + "/write_failure"
			}
			t.Run(name, func(t *testing.T) {
				owner := new115TaskUser(t)
				other := new115TaskUser(t)
				fixtures := []task115Fixture{{owner, tache.StateSucceeded, tool.SubmissionAccepted}}
				if route == "delete_some" || route == "clear_done" {
					fixtures = append(fixtures,
						task115Fixture{owner, tache.StateFailed, tool.SubmissionAccepted},
						task115Fixture{owner, tache.StateCanceled, tool.SubmissionAccepted})
				}
				targetCount := len(fixtures)
				// Collection endpoints must preserve another user's task and
				// states outside the endpoint's selection.
				fixtures = append(fixtures,
					task115Fixture{other, tache.StateSucceeded, tool.SubmissionAccepted},
					task115Fixture{owner, tache.StatePending, tool.SubmissionAccepted})
				if route == "clear_succeeded" {
					fixtures = append(fixtures, task115Fixture{owner, tache.StateFailed, tool.SubmissionAccepted})
				}
				manager, ids := restore115TaskFixtures(t, fixtures...)
				manager.beforeRemove = func(id string) {
					stored, err := db.GetOfflineDownload115Task(id)
					if err != nil {
						t.Fatal(err)
					}
					if stored.DeletedAt == nil {
						t.Error("manager removal preceded the durable tombstone")
					}
					if _, exists := manager.GetByID(id); !exists {
						t.Error("task was removed before the checked Remove call")
					}
				}
				if failWrite {
					fail115TombstoneWrites(t)
				}
				path, body := "/"+route, ""
				switch route {
				case "delete":
					path += "?tid=" + ids[0]
				case "delete_some":
					encoded, err := json.Marshal(ids[:targetCount])
					if err != nil {
						t.Fatal(err)
					}
					body = string(encoded)
				}
				result := request115Task(t, task115Router(manager), owner, http.MethodPost, path, body)
				wantCode := 200
				if failWrite && route != "delete_some" {
					wantCode = 500
				}
				if result.Code != wantCode {
					t.Fatalf("response = %#v, want code %d", result, wantCode)
				}
				if failWrite && route != "delete_some" && !strings.Contains(result.Message, "injected tombstone write failure") {
					t.Errorf("response did not report the tombstone failure: %#v", result)
				}
				if route == "delete_some" {
					var failures map[string]string
					if err := json.Unmarshal(result.Data, &failures); err != nil {
						t.Fatal(err)
					}
					wantFailures := 0
					if failWrite {
						wantFailures = targetCount
					}
					if len(failures) != wantFailures {
						t.Fatalf("batch failures = %v, want %d", failures, wantFailures)
					}
					for _, id := range ids[:targetCount] {
						if failWrite && !strings.Contains(failures[id], "injected tombstone write failure") {
							t.Errorf("failure for %s = %q", id, failures[id])
						}
					}
				}
				wantRemovals := targetCount
				if failWrite {
					wantRemovals = 0
				}
				if len(manager.removed) != wantRemovals {
					t.Errorf("Remove calls = %v, want %d", manager.removed, wantRemovals)
				}
				for i, id := range ids {
					wantDeleted := i < targetCount && !failWrite
					stored, err := db.GetOfflineDownload115Task(id)
					if err != nil {
						t.Fatal(err)
					}
					_, exists := manager.GetByID(id)
					if (stored.DeletedAt != nil) != wantDeleted || exists == wantDeleted {
						t.Errorf("task %s: deleted_at=%v, in manager=%v, want deleted=%v", id, stored.DeletedAt, exists, wantDeleted)
					}
					if stored.CloudDeletedAt != nil || stored.CloudDeleteState != "none" || stored.InfoHash != "fixture-info-hash" {
						t.Errorf("local deletion altered the cloud receipt: %#v", stored)
					}
				}
			})
		}
	}
}

func Test115TaskMissingCreatorAccess(t *testing.T) {
	user := new115TaskUser(t)
	manager, ids := restore115TaskFixtures(t,
		task115Fixture{nil, tache.StateSucceeded, tool.SubmissionAccepted},
		task115Fixture{nil, tache.StatePending, tool.SubmissionAccepted})
	// Recovery makes a live task with a missing creator failed. Move it to a
	// queued state solely to exercise the undone list's authorization predicate.
	pending, exists := manager.GetByID(ids[1])
	if !exists {
		t.Fatal("missing fixture task")
	}
	pending.SetState(tache.StatePending)
	router := task115Router(manager)
	for _, test := range []struct {
		name string
		user *model.User
		code int
	}{
		{"admin", &model.User{ID: user.ID, Role: model.ADMIN}, 200},
		{"ordinary user", user, 404},
		{"typed nil user", nil, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := request115Task(t, router, test.user, http.MethodPost, "/info?tid="+ids[0], "")
			if result.Code != test.code {
				t.Fatalf("info response = %#v, want code %d", result, test.code)
			}
			if test.code == 200 {
				var info TaskInfo
				if err := json.Unmarshal(result.Data, &info); err != nil {
					t.Fatal(err)
				}
				if info.ID != ids[0] || info.Creator != "" || info.CreatorRole != -1 {
					t.Errorf("missing creator info = %#v", info)
				}
			}
			for i, path := range []string{"/done", "/undone"} {
				result := request115Task(t, router, test.user, http.MethodGet, path, "")
				if test.user == nil {
					if result.Code != 401 {
						t.Errorf("%s response = %#v, want code 401", path, result)
					}
					continue
				}
				if result.Code != 200 {
					t.Fatalf("%s response = %#v, want code 200", path, result)
				}
				var tasks []TaskInfo
				if err := json.Unmarshal(result.Data, &tasks); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, task := range tasks {
					if task.ID == ids[i] {
						found = true
					}
				}
				if found != test.user.IsAdmin() {
					t.Errorf("%s contains ownerless task = %v, admin = %v", path, found, test.user.IsAdmin())
				}
			}
		})
	}
}

func Test115TaskRetryRejectsUnknownSubmission(t *testing.T) {
	for _, route := range []string{"retry", "retry_some", "retry_failed"} {
		t.Run(route, func(t *testing.T) {
			owner := new115TaskUser(t)
			manager, ids := restore115TaskFixtures(t, task115Fixture{owner, tache.StateFailed, tool.SubmissionUnknown})
			path, body := "/"+route, ""
			switch route {
			case "retry":
				path += "?tid=" + ids[0]
			case "retry_some":
				encoded, err := json.Marshal(ids)
				if err != nil {
					t.Fatal(err)
				}
				body = string(encoded)
			}
			result := request115Task(t, task115Router(manager), owner, http.MethodPost, path, body)
			if route == "retry_some" {
				var failures map[string]string
				if err := json.Unmarshal(result.Data, &failures); err != nil {
					t.Fatal(err)
				}
				if result.Code != 200 || len(failures) != 1 || !strings.Contains(failures[ids[0]], "unknown") {
					t.Errorf("batch response = %#v, failures = %v", result, failures)
				}
			} else if result.Code != 409 || !strings.Contains(result.Message, "unknown") {
				t.Errorf("response = %#v, want an unknown-result conflict", result)
			}
			if len(manager.retried) != 0 {
				t.Errorf("unknown submission reached manager.Retry: %v", manager.retried)
			}
			download, exists := manager.GetByID(ids[0])
			if !exists || download.GetState() != tache.StateFailed || download.Open115SubmissionState() != tool.SubmissionUnknown {
				t.Fatal("retry changed or removed the unknown task")
			}
			stored, err := db.GetOfflineDownload115Task(ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if stored.SubmissionState != tool.SubmissionUnknown || stored.InfoHash != "" || stored.DeletedAt != nil {
				t.Errorf("retry changed the durable unknown record: %#v", stored)
			}
		})
	}
}
