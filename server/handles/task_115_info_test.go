package handles

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
)

func assert115TaskInfo(t *testing.T, fields map[string]json.RawMessage, submission, hash string) {
	t.Helper()
	for key, want := range map[string]string{
		"tool": tool.Open115Name, "submission_state": submission, "info_hash": hash,
	} {
		value, exists := fields[key]
		if !exists {
			t.Errorf("missing %q in task info", key)
			continue
		}
		var got string
		if err := json.Unmarshal(value, &got); err != nil || got != want {
			t.Errorf("%s = %s, want %q (decode error: %v)", key, value, want, err)
		}
	}
	for _, key := range []string{"storage_id", "account_id", "access_token", "refresh_token", "password", "open115"} {
		if _, exists := fields[key]; exists {
			t.Errorf("internal field %q leaked into task info", key)
		}
	}
}

func Test115TaskMetadataInfoAndLists(t *testing.T) {
	owner := new115TaskUser(t)
	other := new115TaskUser(t)
	fixtures := []task115Fixture{
		{owner, tache.StatePending, tool.SubmissionPending},
		{owner, tache.StatePending, tool.SubmissionAccepted},
		{owner, tache.StateSucceeded, tool.SubmissionAccepted},
		{owner, tache.StateFailed, tool.SubmissionFailed},
		{owner, tache.StateFailed, tool.SubmissionUnknown},
		{owner, tache.StateFailed, tool.SubmissionLegacy},
		{other, tache.StateSucceeded, tool.SubmissionAccepted},
		{nil, tache.StateFailed, tool.SubmissionUnknown},
	}
	manager, ids := restore115TaskFixtures(t, fixtures...)
	router := task115Router(manager)
	admin := &model.User{ID: owner.ID + other.ID + 100, Role: model.ADMIN}
	for index, fixture := range fixtures {
		result := request115Task(t, router, admin, http.MethodPost, "/info?tid="+ids[index], "")
		if result.Code != 200 {
			t.Fatalf("info response = %#v", result)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(result.Data, &fields); err != nil {
			t.Fatal(err)
		}
		hash := ""
		if fixture.submission == tool.SubmissionAccepted {
			hash = "fixture-info-hash"
		}
		assert115TaskInfo(t, fields, fixture.submission, hash)
		if string(fields["id"]) != `"`+ids[index]+`"` {
			t.Errorf("task ID = %s, want %s", fields["id"], ids[index])
		}
	}

	for _, test := range []struct {
		name string
		user *model.User
		done []int
	}{
		{"owner", owner, []int{2, 3, 4, 5}},
		{"other", other, []int{6}},
		{"admin", admin, []int{2, 3, 4, 5, 6, 7}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, route := range []string{"done", "undone"} {
				wantIndexes := test.done
				if route == "undone" {
					wantIndexes = nil
					if test.user == owner || test.user == admin {
						wantIndexes = []int{0, 1}
					}
				}
				result := request115Task(t, router, test.user, http.MethodGet, "/"+route, "")
				if result.Code != 200 {
					t.Fatalf("%s response = %#v", route, result)
				}
				var infos []map[string]json.RawMessage
				if err := json.Unmarshal(result.Data, &infos); err != nil {
					t.Fatal(err)
				}
				if len(infos) != len(wantIndexes) || infos == nil {
					t.Fatalf("%s list = %s, want %d tasks", route, result.Data, len(wantIndexes))
				}
				byID := make(map[string]map[string]json.RawMessage, len(infos))
				for _, info := range infos {
					var id string
					if err := json.Unmarshal(info["id"], &id); err != nil {
						t.Fatal(err)
					}
					byID[id] = info
				}
				for _, index := range wantIndexes {
					hash := ""
					if fixtures[index].submission == tool.SubmissionAccepted {
						hash = "fixture-info-hash"
					}
					assert115TaskInfo(t, byID[ids[index]], fixtures[index].submission, hash)
				}
			}
		})
	}
	for _, id := range []string{ids[0], ids[7]} {
		result := request115Task(t, router, other, http.MethodPost, "/info?tid="+id, "")
		if result.Code != 404 || string(result.Data) != "null" {
			t.Fatalf("another user's metadata was exposed: %#v", result)
		}
	}
}

func TestOtherDownloadTaskInfoKeepsResponseShape(t *testing.T) {
	owner := new115TaskUser(t)
	manager := &observed115TaskManager{Manager: tache.NewManager[*tool.DownloadTask](tache.WithRunning(false), tache.WithWorks(0))}
	download := &tool.DownloadTask{
		TaskExtension: task.TaskExtension{Creator: owner},
		Toolname:      "aria2", Url: "https://example.com/file", DstDirPath: "/Downloads",
	}
	manager.Add(download)
	result := request115Task(t, task115Router(manager), owner, http.MethodPost, "/info?tid="+download.GetID(), "")
	if result.Code != 200 {
		t.Fatalf("info response = %#v", result)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.Data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tool", "info_hash", "submission_state"} {
		if _, exists := fields[key]; exists {
			t.Errorf("115 field %q added to another tool", key)
		}
	}
	for _, key := range []string{"id", "name", "creator", "creator_role", "state", "status", "progress", "start_time", "end_time", "total_bytes", "error"} {
		if _, exists := fields[key]; !exists {
			t.Errorf("original field %q missing", key)
		}
	}
}
