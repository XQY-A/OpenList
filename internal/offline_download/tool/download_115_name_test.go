package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenListTeam/tache"
)

func TestDownload115CloudNamePersistsAndSurvivesRetry(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	name := "云端电影 [中字] (2026).mkv"
	client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
		return &Open115TaskStatus{
			Name: "  " + name + "  ", State: -1,
			Status: Status{Err: errors.New("cloud download failed")},
		}, nil
	}
	worker := tache.Worker[*DownloadTask]{}
	worker.Execute(download)
	if download.GetState() != tache.StateFailed {
		t.Fatalf("state = %v, want failed task available for retry", download.GetState())
	}
	want := fmt.Sprintf("download %s to (%s)", name, download.DstDirPath)
	if download.GetName() != want || download.Get115TaskInfo().SourceURL != download.Url {
		t.Fatalf("cloud name changed the source URL: name=%q, info=%+v", download.GetName(), download.Get115TaskInfo())
	}
	if snapshot := decodeDownload115Snapshot(t, store.snapshot()); snapshot.CloudName != name || snapshot.URL != download.Url {
		t.Fatalf("name and original URL were not persisted separately: %+v", snapshot)
	}
	client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
		return &Open115TaskStatus{Name: " \t ", State: 2, Status: Status{Completed: true}}, nil
	}
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	var next *DownloadTask
	if err := download.RetryWith(func(retried *DownloadTask) {
		next = retried
		manager.Add(retried)
	}); err != nil {
		t.Fatal(err)
	}
	if next.GetName() != want || next.Url != download.Url {
		t.Fatal("retry discarded the saved cloud name or original URL")
	}
	worker.Execute(next)
	if next.GetName() != want || decodeDownload115Snapshot(t, store.snapshot()).CloudName != name {
		t.Fatal("a blank cloud response erased the last known name")
	}
	if next.GetState() != tache.StateSucceeded || client.submissions != 1 || len(client.lookups) != 2 {
		t.Fatalf("name handling added a cloud request: state=%v, submits=%d, lookups=%d", next.GetState(), client.submissions, len(client.lookups))
	}
}

func TestDownload115BlankCloudNameKeepsURL(t *testing.T) {
	for i, name := range []string{"", " \t\n ", "\u3000"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			download, store, client := newDownload115TestTask(t)
			initDownload115TestTask(t, download)
			client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
				return &Open115TaskStatus{Name: name, State: 2, Status: Status{Completed: true}}, nil
			}
			worker := tache.Worker[*DownloadTask]{}
			worker.Execute(download)
			want := fmt.Sprintf("download %s to (%s)", download.Url, download.DstDirPath)
			if download.GetName() != want || strings.Contains(store.snapshot().Snapshot, `"cloud_name"`) {
				t.Fatalf("blank cloud name replaced the URL: %q, %s", download.GetName(), store.snapshot().Snapshot)
			}
		})
	}
	ordinary := &DownloadTask{Toolname: "aria2", Url: "https://example.com/file", DstDirPath: "/downloads", cloudName: "115 name"}
	if ordinary.GetName() != "download https://example.com/file to (/downloads)" {
		t.Fatal("another download tool used a 115 cloud name")
	}
}

func TestDownloadTaskManagerRestores115CloudNameAndOldSnapshots(t *testing.T) {
	_, creator := setupDownloadManagerDB(t)
	for i, name := range []string{"", "云端名称 (2026).mkv"} {
		record := manager115Record(t, fmt.Sprintf("cloud-name-%d", i), creator.ID, tache.StateSucceeded, SubmissionAccepted)
		snapshot := decodeDownload115Snapshot(t, &record)
		snapshot.CloudName = name
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		record.Snapshot = string(encoded)
		createManager115Record(t, record)
	}
	manager, err := NewDownloadTaskManager(1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"magnet:?xt=urn:btih:example", "云端名称 (2026).mkv"} {
		download, ok := manager.GetByID(fmt.Sprintf("cloud-name-%d", i))
		if !ok || download.GetName() != fmt.Sprintf("download %s to (/115/downloads)", name) {
			t.Fatalf("restored name %d was lost: %+v", i, download)
		}
		if download.GetState() != tache.StateSucceeded || download.Get115TaskInfo().SourceURL != "magnet:?xt=urn:btih:example" {
			t.Fatal("restoring a name changed the terminal state or source URL")
		}
	}
}
