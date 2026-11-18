package tool

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

func TestDownload115InfoCompatibility(t *testing.T) {
	for _, name := range []string{"", "115 Cloud", "SimpleHttp", "aria2"} {
		download := &DownloadTask{Toolname: name}
		if info := download.Get115TaskInfo(); info != nil {
			t.Fatalf("%q unexpectedly exposed 115 metadata: %+v", name, info)
		}
	}
	legacy := &DownloadTask{Toolname: Open115Name, GID: "unverified-old-gid", Url: "magnet:?xt=urn:btih:original"}
	info := legacy.Get115TaskInfo()
	if info.Tool != Open115Name || info.InfoHash != "" || info.SubmissionState != SubmissionLegacy || info.SourceURL != legacy.Url {
		t.Fatalf("legacy metadata = %+v; must not invent a verified receipt", info)
	}
}

func TestDownload115InfoDuringAcceptance(t *testing.T) {
	// Exercise the public read against the same synchronous transition used by
	// a worker receiving its cloud receipt. Each task has its own opaque hash.
	const count = 64
	var workers sync.WaitGroup
	for i := range count {
		download := &DownloadTask{Toolname: Open115Name, open115Store: &download115TestStore{}}
		if err := download.init115(fmt.Sprintf("receipt-%d", i)); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("Original-ReCeIpT:%d+/=", i)
		start := make(chan struct{})
		workers.Add(2)
		go func() {
			defer workers.Done()
			<-start
			if err := download.set115Submission(SubmissionAccepted, hash, Open115Reference{StorageID: 41, AccountID: "owner"}); err != nil {
				t.Errorf("acceptance: %v", err)
			}
		}()
		go func() {
			defer workers.Done()
			<-start
			for range 100 {
				info := download.Get115TaskInfo()
				if info.Tool != Open115Name ||
					(info.SubmissionState == SubmissionPending && info.InfoHash != "") ||
					(info.SubmissionState == SubmissionAccepted && info.InfoHash != hash) ||
					(info.SubmissionState != SubmissionPending && info.SubmissionState != SubmissionAccepted) {
					t.Errorf("inconsistent receipt: %+v, expected %q", info, hash)
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
	}
	workers.Wait()
}
