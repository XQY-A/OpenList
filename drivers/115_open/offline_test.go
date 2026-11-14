package _115_open

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/OpenListTeam/115-sdk-go"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type offlineTestTransport func(*http.Request) (*http.Response, error)

func (f offlineTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newOfflineTestDriver(handler http.HandlerFunc) *Open115 {
	d := &Open115{client: sdk.New(sdk.WithAccessToken("test-access"))}
	d.accountID.Store(12345)
	d.client.SetHttpClient(&http.Client{Transport: offlineTestTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
		return w.Result(), nil
	})})
	return d
}

func TestAddOfflineTaskReceipts(t *testing.T) {
	const uri = "ed2k://|file|example.mkv|123|0123456789ABCDEF0123456789ABCDEF|/"
	tests := []struct {
		name     string
		body     string
		hash     string
		rejected bool
	}{
		{"accepted", `{"state":true,"data":[{"state":true,"info_hash":"Cloud-ORIGINAL-hash","url":` + strconv.Quote(uri) + `}]}`, "Cloud-ORIGINAL-hash", false},
		{"accepted unique without URL", `{"state":true,"data":[{"state":true,"info_hash":"raw-hash"}]}`, "raw-hash", false},
		{"refused URL", `{"state":true,"data":[{"state":false,"code":10008,"message":"unsupported link","url":` + strconv.Quote(uri) + `}]}`, "", true},
		{"refused envelope", `{"state":false,"code":10008,"message":"quota exceeded"}`, "", true},
		{"empty array", `{"state":true,"data":[]}`, "", false},
		{"null receipt", `{"state":true,"data":null}`, "", false},
		{"multiple receipts", `{"state":true,"data":[{"state":true,"info_hash":"a"},{"state":true,"info_hash":"b"}]}`, "", false},
		{"accepted without hash", `{"state":true,"data":[{"state":true,"url":` + strconv.Quote(uri) + `}]}`, "", false},
		{"missing item state", `{"state":true,"data":[{"url":` + strconv.Quote(uri) + `}]}`, "", false},
		{"refusal without URL", `{"state":true,"data":[{"state":false,"code":10008}]}`, "", false},
		{"refusal for another URL", `{"state":true,"data":[{"state":false,"code":10008,"url":"another"}]}`, "", false},
		{"acceptance for another URL", `{"state":true,"data":[{"state":true,"info_hash":"another","url":"another"}]}`, "", false},
		{"contradictory receipt", `{"state":true,"data":[{"state":false,"info_hash":"already-there","url":` + strconv.Quote(uri) + `}]}`, "", false},
		{"zero value SDK error", `{}`, "", false},
		{"malformed JSON", `{"state":true,"data":`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			d := newOfflineTestDriver(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.String() != sdk.ApiAddOffline || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("urls") != uri || r.Form.Get("wp_path_id") != "987" {
					t.Errorf("submission form = %v", r.Form)
				}
				_, _ = io.WriteString(w, tt.body)
			})
			got, err := d.AddOfflineTaskForAccount(context.Background(), "12345", uri, &model.Object{ID: "987", IsFolder: true})
			if got != tt.hash || (err == nil) != (tt.hash != "") {
				t.Fatalf("hash = %q, error = %v, want hash %q", got, err, tt.hash)
			}
			var rejected *OfflineTaskRejectedError
			if errors.As(err, &rejected) != tt.rejected {
				t.Fatalf("rejection classification = %v, want %v: %v", rejected != nil, tt.rejected, err)
			}
			if calls != 1 {
				t.Fatalf("submission requests = %d, want one", calls)
			}
		})
	}
}

func TestAddOfflineTaskTransportErrorIsUnknown(t *testing.T) {
	d := newOfflineTestDriver(nil)
	lostReceipt := errors.New("connection closed after request")
	d.client.SetHttpClient(&http.Client{Transport: offlineTestTransport(func(*http.Request) (*http.Response, error) {
		return nil, lostReceipt
	})})
	hash, err := d.AddOfflineTask(context.Background(), "magnet:?xt=urn:btih:original", &model.Object{ID: "1"})
	var rejected *OfflineTaskRejectedError
	if hash != "" || !errors.Is(err, lostReceipt) || errors.As(err, &rejected) {
		t.Fatalf("lost receipt classified as hash=%q, error=%v", hash, err)
	}
}

func TestAddOfflineTaskValidationDoesNotSend(t *testing.T) {
	d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid task was sent") })
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx context.Context
		uri string
		dir model.Obj
	}{
		{context.Background(), "", &model.Object{ID: "1"}},
		{context.Background(), "first\nsecond", &model.Object{ID: "1"}},
		{context.Background(), "first", nil},
		{canceled, "first", &model.Object{ID: "1"}},
	} {
		_, err := d.AddOfflineTask(tc.ctx, tc.uri, tc.dir)
		var rejected *OfflineTaskRejectedError
		if !errors.As(err, &rejected) {
			t.Errorf("pre-send failure must be definite: %v", err)
		}
	}
}

func TestOfflineRequestsRejectChangedAccount(t *testing.T) {
	d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { t.Fatal("request sent with mismatched account") })
	for _, expected := range []string{"", "another-account"} {
		if _, err := d.AddOfflineTaskForAccount(context.Background(), expected, "https://example.invalid/file", &model.Object{ID: "1"}); err == nil {
			t.Error("submission accepted mismatched account")
		}
		if _, err := d.GetOfflineTaskForAccount(context.Background(), expected, "hash"); err == nil {
			t.Error("query accepted mismatched account")
		}
		if err := d.DeleteOfflineTaskForAccount(context.Background(), expected, "hash"); err == nil {
			t.Error("deletion accepted mismatched account")
		}
		if err := d.WithOfflineAccount(context.Background(), expected, func() error {
			t.Error("directory preparation accepted mismatched account")
			return nil
		}); err == nil {
			t.Error("directory preparation did not report mismatched account")
		}
	}
	d.accountID.Store(0)
	if _, err := d.GetOfflineTaskForAccount(context.Background(), "12345", "hash"); err == nil {
		t.Error("query accepted an uninitialized account")
	}
	if err := d.WithOfflineAccount(context.Background(), "12345", func() error {
		t.Error("directory preparation accepted an uninitialized account")
		return nil
	}); err == nil {
		t.Error("directory preparation did not report uninitialized account")
	}
}

func TestWithOfflineAccountValidatesContextAndCallback(t *testing.T) {
	d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { t.Error("unexpected cloud request") })
	callbackErr := errors.New("directory preparation failed")
	if err := d.WithOfflineAccount(context.Background(), "12345", func() error {
		if d.offlineMu.TryLock() {
			d.offlineMu.Unlock()
			t.Error("account was not protected during directory preparation")
		}
		return callbackErr
	}); !errors.Is(err, callbackErr) {
		t.Fatalf("callback error was lost: %v", err)
	}
	if err := d.WithOfflineAccount(context.Background(), "12345", nil); err == nil {
		t.Error("nil callback was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.WithOfflineAccount(ctx, "12345", func() error {
		t.Error("directory preparation continued after cancellation")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was lost: %v", err)
	}
	// A canceled caller must not wait for an already-held initialization lock.
	d.offlineMu.Lock()
	result := make(chan error, 1)
	go func() {
		result <- d.WithOfflineAccount(ctx, "12345", func() error { return nil })
	}()
	select {
	case err := <-result:
		d.offlineMu.Unlock()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled operation returned %v", err)
		}
	case <-time.After(time.Second):
		d.offlineMu.Unlock()
		t.Fatal("canceled operation waited for the account lock")
	}
}

func TestDeleteOfflineTaskForAccountKeepsFiles(t *testing.T) {
	d := newOfflineTestDriver(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != sdk.ApiDeleteOffline || r.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("info_hash") != "Cloud-ORIGINAL-hash" || r.Form.Get("del_source_file") != "0" {
			t.Fatalf("unsafe delete form: %v", r.Form)
		}
		_, _ = io.WriteString(w, `{"state":true,"data":[]}`)
	})
	if err := d.DeleteOfflineTaskForAccount(context.Background(), "12345", "Cloud-ORIGINAL-hash"); err != nil {
		t.Fatal(err)
	}
}

func TestGetOfflineTaskSendsPageQuery(t *testing.T) {
	var pages []int
	d := newOfflineTestDriver(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open/offline/get_task_list" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Fatalf("page must be sent in query parameters: %s", r.URL)
		}
		pages = append(pages, page)
		var tasks []sdk.OfflineTask
		switch page {
		case 1:
			tasks = []sdk.OfflineTask{{InfoHash: "a"}, {InfoHash: "b"}}
		case 2:
			tasks = []sdk.OfflineTask{{InfoHash: "c"}, {InfoHash: "d"}}
		case 3:
			tasks = []sdk.OfflineTask{{InfoHash: "wanted", Status: 2, PercentDone: 100, Size: 123}}
		default:
			t.Fatalf("unexpected page %d", page)
		}
		_ = json.NewEncoder(w).Encode(sdk.Resp[sdk.OfflineTaskListResp]{State: true, Data: sdk.OfflineTaskListResp{
			Page: page, PageCount: 3, Count: 5, Tasks: tasks,
		}})
	})
	got, err := d.GetOfflineTaskForAccount(context.Background(), "12345", "wanted")
	if err != nil || got == nil || got.InfoHash != "wanted" || got.Status != 2 || got.Size != 123 {
		t.Fatalf("lookup = %+v, error = %v", got, err)
	}
	if !reflect.DeepEqual(pages, []int{1, 2, 3}) {
		t.Fatalf("queried pages = %v", pages)
	}
}

func TestFindOfflineTaskRequiresCompletePages(t *testing.T) {
	page := func(n, pages, count int, hashes ...string) *sdk.OfflineTaskListResp {
		resp := &sdk.OfflineTaskListResp{Page: n, PageCount: pages, Count: count}
		for _, hash := range hashes {
			resp.Tasks = append(resp.Tasks, sdk.OfflineTask{InfoHash: hash})
		}
		return resp
	}
	tests := []struct {
		name    string
		pages   []*sdk.OfflineTaskListResp
		wantErr bool
		found   bool
	}{
		{"empty account", []*sdk.OfflineTaskListResp{page(1, 0, 0)}, false, false},
		{"empty page one", []*sdk.OfflineTaskListResp{page(1, 1, 0)}, false, false},
		{"complete absence", []*sdk.OfflineTaskListResp{page(1, 2, 3, "a", "b"), page(2, 2, 3, "c")}, false, false},
		{"found early", []*sdk.OfflineTaskListResp{page(1, 2, 3, "wanted", "b")}, false, true},
		{"nil page", []*sdk.OfflineTaskListResp{nil}, true, false},
		{"zero value page", []*sdk.OfflineTaskListResp{{}}, true, false},
		{"wrong page number", []*sdk.OfflineTaskListResp{page(2, 2, 2, "a")}, true, false},
		{"negative count", []*sdk.OfflineTaskListResp{page(1, 1, -1)}, true, false},
		{"negative pages", []*sdk.OfflineTaskListResp{page(1, -1, 0)}, true, false},
		{"contradictory empty count", []*sdk.OfflineTaskListResp{page(1, 1, 0, "a")}, true, false},
		{"zero pages with data", []*sdk.OfflineTaskListResp{page(1, 0, 1, "a")}, true, false},
		{"empty intermediate page", []*sdk.OfflineTaskListResp{page(1, 2, 2, "a"), page(2, 2, 2)}, true, false},
		{"count changed", []*sdk.OfflineTaskListResp{page(1, 2, 2, "a"), page(2, 2, 3, "b")}, true, false},
		{"page count changed", []*sdk.OfflineTaskListResp{page(1, 2, 2, "a"), page(2, 3, 2, "b")}, true, false},
		{"repeated page", []*sdk.OfflineTaskListResp{page(1, 2, 2, "a"), page(2, 2, 2, "a")}, true, false},
		{"empty item hash", []*sdk.OfflineTaskListResp{page(1, 1, 1, "")}, true, false},
		{"too few items", []*sdk.OfflineTaskListResp{page(1, 1, 2, "a")}, true, false},
		{"too many items", []*sdk.OfflineTaskListResp{page(1, 1, 1, "a", "b")}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			got, err := findOfflineTask(context.Background(), "wanted", func(_ context.Context, p int64) (*sdk.OfflineTaskListResp, error) {
				calls++
				if p < 1 || int(p) > len(tt.pages) {
					t.Fatalf("unnecessary query of page %d", p)
				}
				return tt.pages[p-1], nil
			})
			if (err != nil) != tt.wantErr || (got != nil) != tt.found {
				t.Fatalf("task=%+v error=%v; want found=%v error=%v", got, err, tt.found, tt.wantErr)
			}
			if !tt.wantErr && calls != len(tt.pages) {
				t.Fatalf("only queried %d of %d expected pages", calls, len(tt.pages))
			}
		})
	}
}

func TestFindOfflineTaskDoesNotTurnQueryFailureIntoAbsence(t *testing.T) {
	queryErr := errors.New("page unavailable")
	_, err := findOfflineTask(context.Background(), "wanted", func(_ context.Context, p int64) (*sdk.OfflineTaskListResp, error) {
		if p == 1 {
			return &sdk.OfflineTaskListResp{Page: 1, PageCount: 2, Count: 2, Tasks: []sdk.OfflineTask{{InfoHash: "a"}}}, nil
		}
		return nil, queryErr
	})
	if !errors.Is(err, queryErr) {
		t.Fatalf("missing page error lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = findOfflineTask(ctx, "wanted", func(context.Context, int64) (*sdk.OfflineTaskListResp, error) {
		t.Fatal("query sent after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query returned %v", err)
	}
}

func TestOfflineAccountChangeWaitsForBoundOperation(t *testing.T) {
	for _, test := range []struct{ action, operation string }{
		{"drop", "query"}, {"init", "query"},
		{"drop", "prepare"}, {"init", "prepare"},
	} {
		t.Run(test.action+"/"+test.operation, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			var calls atomic.Int32
			d := newOfflineTestDriver(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				close(started)
				<-release
				_, _ = io.WriteString(w, `{"state":true,"data":{"page":1,"page_count":1,"count":1,"tasks":[{"info_hash":"wanted","status":2}]}}`)
			})
			queryDone := make(chan error, 1)
			go func() {
				if test.operation == "query" {
					_, err := d.GetOfflineTaskForAccount(context.Background(), "12345", "wanted")
					queryDone <- err
					return
				}
				queryDone <- d.WithOfflineAccount(context.Background(), "12345", func() error {
					close(started)
					<-release
					return nil
				})
			}()
			<-started
			actionStarted := make(chan struct{})
			actionDone := make(chan error, 1)
			go func() {
				close(actionStarted)
				if test.action == "drop" {
					actionDone <- d.Drop(context.Background())
				} else {
					// Missing credentials fail before Init can make any request.
					actionDone <- d.Init(context.Background())
				}
			}()
			<-actionStarted
			select {
			case err := <-actionDone:
				close(release)
				t.Fatalf("account changed while its operation was in flight: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			if d.AccountID() != "12345" {
				t.Error("account ID changed before the bound request completed")
			}
			close(release)
			if err := <-queryDone; err != nil {
				t.Fatal(err)
			}
			if err := <-actionDone; (err != nil) != (test.action == "init") {
				t.Fatalf("%s returned %v", test.action, err)
			}
			if got := d.AccountID(); got != "" {
				t.Fatalf("%s left stale account ID %q", test.action, got)
			}
			if _, err := d.GetOfflineTaskForAccount(context.Background(), "12345", "wanted"); err == nil {
				t.Error("new request accepted stale account binding")
			}
			if err := d.WithOfflineAccount(context.Background(), "12345", func() error {
				t.Error("directory preparation accepted stale account binding")
				return nil
			}); err == nil {
				t.Error("directory preparation did not report stale account binding")
			}
			var wantCalls int32
			if test.operation == "query" {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("requests = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestOfflineLookupRejectsMalformedHTTPResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"state":true,"data":{}}`, `{"state":true,"data":null}`} {
		t.Run(fmt.Sprintf("response-%d", len(body)), func(t *testing.T) {
			d := newOfflineTestDriver(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, strings.NewReader(body)) })
			got, err := d.GetOfflineTask(context.Background(), "wanted")
			if err == nil || got != nil {
				t.Fatalf("invalid response declared absence: task=%+v error=%v", got, err)
			}
		})
	}
}
