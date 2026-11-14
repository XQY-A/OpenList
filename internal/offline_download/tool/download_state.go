package tool

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
)

// tache.Base is not synchronized. Keep its persistence callback unset and call
// the manager's callback after releasing mu, since it may marshal this task.
func (t *DownloadTask) SetPersist(persist func()) {
	t.mu.Lock()
	t.persist = persist
	t.mu.Unlock()
}

func (t *DownloadTask) Persist() {
	if t.Is115Open() {
		_ = t.save115()
		return
	}
	t.mu.RLock()
	persist := t.persist
	t.mu.RUnlock()
	if persist != nil {
		persist()
	}
}

func (t *DownloadTask) Persistable() bool { return !t.Is115Open() }

func (t *DownloadTask) SetID(id string) {
	t.mu.Lock()
	t.Base.SetID(id)
	t.mu.Unlock()
	t.Persist()
}

func (t *DownloadTask) GetID() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Base.GetID()
}

func (t *DownloadTask) SetState(state tache.State) {
	t.mu.Lock()
	if t.retired {
		t.mu.Unlock()
		return
	}
	t.Base.SetState(state)
	if isDownloadTerminal(state) && t.EndTime == nil {
		now := time.Now()
		t.EndTime = &now
	}
	t.mu.Unlock()
	t.Persist()
}

func (t *DownloadTask) GetState() tache.State {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Base.GetState()
}

func (t *DownloadTask) SetErr(err error) {
	t.mu.Lock()
	if t.retired {
		t.mu.Unlock()
		return
	}
	t.Base.SetErr(err)
	t.mu.Unlock()
	t.Persist()
}

func (t *DownloadTask) GetErr() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return errors.Join(t.Base.GetErr(), t.persistErr)
}

func (t *DownloadTask) SetProgress(progress float64) {
	t.mu.Lock()
	t.Base.SetProgress(progress)
	t.mu.Unlock()
	t.Persist()
}

func (t *DownloadTask) GetProgress() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Base.GetProgress()
}

func (t *DownloadTask) SetCtx(ctx context.Context) {
	t.mu.Lock()
	t.TaskExtension.SetCtx(ctx)
	t.mu.Unlock()
}

func (t *DownloadTask) Ctx() context.Context {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Base.Ctx()
}

func (t *DownloadTask) CtxDone() <-chan struct{} { return t.Ctx().Done() }

func (t *DownloadTask) SetCancelFunc(cancel context.CancelFunc) {
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
}

func (t *DownloadTask) Cancel() {
	t.mu.Lock()
	if t.retired || isDownloadTerminal(t.Base.GetState()) {
		t.mu.Unlock()
		return
	}
	t.Base.SetState(tache.StateCanceling)
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// A queued task can remain Canceling until a worker becomes available. Its
	// durable snapshot is already Canceled, so a restart cannot submit it.
	t.Persist()
}

func (t *DownloadTask) SetRetry(retry, maxRetry int) {
	t.mu.Lock()
	t.Base.SetRetry(retry, maxRetry)
	if retry == 0 && conf.Conf.Tasks.AllowRetryCanceled && t.Base.Ctx() != nil && t.Base.Ctx().Err() != nil {
		ctx, cancel := context.WithCancel(context.Background())
		t.TaskExtension.SetCtx(ctx)
		t.cancel = cancel
	}
	t.mu.Unlock()
}

func (t *DownloadTask) GetRetry() (int, int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Base.GetRetry()
}

func (t *DownloadTask) SetCreator(creator *model.User) {
	t.mu.Lock()
	t.Creator = creator
	t.mu.Unlock()
	t.Persist()
}

func (t *DownloadTask) GetCreator() *model.User {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Creator
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (t *DownloadTask) SetStartTime(value time.Time) {
	t.mu.Lock()
	t.StartTime = &value
	t.mu.Unlock()
}

func (t *DownloadTask) GetStartTime() *time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return copyTime(t.StartTime)
}

func (t *DownloadTask) SetEndTime(value time.Time) {
	t.mu.Lock()
	t.EndTime = &value
	t.mu.Unlock()
}

func (t *DownloadTask) GetEndTime() *time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return copyTime(t.EndTime)
}

func (t *DownloadTask) ClearEndTime() {
	t.mu.Lock()
	t.EndTime = nil
	t.mu.Unlock()
}

func (t *DownloadTask) SetTotalBytes(value int64) {
	t.mu.Lock()
	t.TotalBytes = value
	t.mu.Unlock()
}

func (t *DownloadTask) GetTotalBytes() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.TotalBytes
}

func (t *DownloadTask) SetStatus(value string) {
	t.mu.Lock()
	t.Status = value
	t.mu.Unlock()
}

func (t *DownloadTask) GetStatus() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status
}

// Preserve the existing JSON format for other tools without racing task state
// updates. 115 Open uses its dedicated, credential-free snapshot instead.
func (t *DownloadTask) MarshalJSON() ([]byte, error) {
	t.mu.RLock()
	value := struct {
		task.TaskExtension
		URL          string       `json:"url"`
		DstDirPath   string       `json:"dst_dir_path"`
		TempDir      string       `json:"temp_dir"`
		DeletePolicy DeletePolicy `json:"delete_policy"`
		Toolname     string       `json:"toolname"`
	}{t.TaskExtension, t.Url, t.DstDirPath, t.TempDir, t.DeletePolicy, t.Toolname}
	t.mu.RUnlock()
	return json.Marshal(value)
}

func isDownloadTerminal(state tache.State) bool {
	return state == tache.StateSucceeded || state == tache.StateFailed || state == tache.StateCanceled
}
