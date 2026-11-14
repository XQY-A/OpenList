package handles

import (
	"errors"
	"math"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
)

type TaskInfo struct {
	*tool.Open115TaskInfo
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Creator     string      `json:"creator"`
	CreatorRole int         `json:"creator_role"`
	State       tache.State `json:"state"`
	Status      string      `json:"status"`
	Progress    float64     `json:"progress"`
	StartTime   *time.Time  `json:"start_time"`
	EndTime     *time.Time  `json:"end_time"`
	TotalBytes  int64       `json:"total_bytes"`
	Error       string      `json:"error"`
}

func getTaskInfo[T task.TaskExtensionInfo](task T) TaskInfo {
	errMsg := ""
	if err := task.GetErr(); err != nil {
		errMsg = err.Error()
	}
	progress := task.GetProgress()
	// if progress is NaN, set it to 100
	if math.IsNaN(progress) {
		progress = 100
	}
	creatorName := ""
	creatorRole := -1
	if creator := task.GetCreator(); creator != nil {
		creatorName = creator.Username
		creatorRole = creator.Role
	}
	info := TaskInfo{
		ID:          task.GetID(),
		Name:        task.GetName(),
		Creator:     creatorName,
		CreatorRole: creatorRole,
		State:       task.GetState(),
		Status:      task.GetStatus(),
		Progress:    progress,
		StartTime:   task.GetStartTime(),
		EndTime:     task.GetEndTime(),
		TotalBytes:  task.GetTotalBytes(),
		Error:       errMsg,
	}
	if download, ok := any(task).(*tool.DownloadTask); ok {
		info.Open115TaskInfo = download.Get115TaskInfo()
	}
	return info
}

func getTaskInfos[T task.TaskExtensionInfo](tasks []T) []TaskInfo {
	return utils.MustSliceConvert(tasks, getTaskInfo[T])
}

func argsContains[T comparable](v T, slice ...T) bool {
	return utils.SliceContains(slice, v)
}

func getUserInfo(c *gin.Context) (bool, uint, bool) {
	if user, ok := c.Request.Context().Value(conf.UserKey).(*model.User); ok && user != nil {
		return user.IsAdmin(), user.ID, true
	} else {
		return false, 0, false
	}
}

func canAccessTask(t task.TaskExtensionInfo, isAdmin bool, uid uint) bool {
	creator := t.GetCreator()
	return isAdmin || (creator != nil && uid == creator.ID)
}

func removeManagedTask[T task.TaskExtensionInfo](manager task.Manager[T], t T) error {
	if durable, ok := any(t).(interface{ RemoveLocal() error }); ok {
		if err := durable.RemoveLocal(); err != nil {
			return err
		}
	}
	manager.Remove(t.GetID())
	return nil
}

func retryManagedTask[T task.TaskExtensionInfo](manager task.Manager[T], t T) error {
	if download, ok := any(t).(*tool.DownloadTask); ok {
		return download.RetryWith(func(next *tool.DownloadTask) {
			if next == download {
				manager.Retry(t.GetID())
				return
			}
			manager.Add(any(next).(T))
		})
	}
	manager.Retry(t.GetID())
	return nil
}

func taskOperationError(c *gin.Context, err error) {
	code := 500
	if errors.Is(err, tool.ErrTaskConflict) {
		code = 409
	}
	common.ErrorResp(c, err, code)
}

func getTargetedHandler[T task.TaskExtensionInfo](manager task.Manager[T], callback func(c *gin.Context, task T)) gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		t, ok := manager.GetByID(c.Query("tid"))
		if !ok {
			common.ErrorStrResp(c, "task not found", 404)
			return
		}
		if !canAccessTask(t, isAdmin, uid) {
			// to avoid an attacker using error messages to guess valid TID, return a 404 rather than a 403
			common.ErrorStrResp(c, "task not found", 404)
			return
		}
		callback(c, t)
	}
}

func getBatchHandler[T task.TaskExtensionInfo](manager task.Manager[T], callback func(task T) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		var tids []string
		if err := c.ShouldBind(&tids); err != nil {
			common.ErrorStrResp(c, "invalid request format", 400)
			return
		}
		retErrs := make(map[string]string)
		for _, tid := range tids {
			t, ok := manager.GetByID(tid)
			if !ok || !canAccessTask(t, isAdmin, uid) {
				retErrs[tid] = "task not found"
				continue
			}
			if err := callback(t); err != nil {
				retErrs[tid] = err.Error()
			}
		}
		common.SuccessResp(c, retErrs)
	}
}

func taskRoute[T task.TaskExtensionInfo](g *gin.RouterGroup, manager task.Manager[T], allowRetry ...bool) {
	canRetry := len(allowRetry) == 0 || allowRetry[0]
	g.GET("/undone", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {
			// avoid directly passing the user object into the function to reduce closure size
			return canAccessTask(task, isAdmin, uid) &&
				argsContains(task.GetState(), tache.StatePending, tache.StateRunning, tache.StateCanceling,
					tache.StateErrored, tache.StateFailing, tache.StateWaitingRetry, tache.StateBeforeRetry)
		})))
	})
	g.GET("/done", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {
			return canAccessTask(task, isAdmin, uid) &&
				argsContains(task.GetState(), tache.StateCanceled, tache.StateFailed, tache.StateSucceeded)
		})))
	})
	g.POST("/info", getTargetedHandler(manager, func(c *gin.Context, task T) {
		common.SuccessResp(c, getTaskInfo(task))
	}))
	g.POST("/cancel", getTargetedHandler(manager, func(c *gin.Context, task T) {
		manager.Cancel(task.GetID())
		common.SuccessResp(c)
	}))
	g.POST("/delete", getTargetedHandler(manager, func(c *gin.Context, task T) {
		if err := removeManagedTask(manager, task); err != nil {
			taskOperationError(c, err)
			return
		}
		common.SuccessResp(c)
	}))
	if canRetry {
		g.POST("/retry", getTargetedHandler(manager, func(c *gin.Context, task T) {
			if err := retryManagedTask(manager, task); err != nil {
				taskOperationError(c, err)
				return
			}
			common.SuccessResp(c)
		}))
	}
	g.POST("/cancel_some", getBatchHandler(manager, func(task T) error {
		manager.Cancel(task.GetID())
		return nil
	}))
	g.POST("/delete_some", getBatchHandler(manager, func(task T) error {
		return removeManagedTask(manager, task)
	}))
	if canRetry {
		g.POST("/retry_some", getBatchHandler(manager, func(task T) error {
			return retryManagedTask(manager, task)
		}))
	}
	g.POST("/clear_done", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		tasks := manager.GetByCondition(func(task T) bool {
			return canAccessTask(task, isAdmin, uid) &&
				argsContains(task.GetState(), tache.StateCanceled, tache.StateFailed, tache.StateSucceeded)
		})
		var failures []error
		for _, t := range tasks {
			if err := removeManagedTask(manager, t); err != nil {
				failures = append(failures, err)
			}
		}
		if err := errors.Join(failures...); err != nil {
			taskOperationError(c, err)
			return
		}
		common.SuccessResp(c)
	})
	g.POST("/clear_succeeded", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		tasks := manager.GetByCondition(func(task T) bool {
			return canAccessTask(task, isAdmin, uid) && task.GetState() == tache.StateSucceeded
		})
		var failures []error
		for _, t := range tasks {
			if err := removeManagedTask(manager, t); err != nil {
				failures = append(failures, err)
			}
		}
		if err := errors.Join(failures...); err != nil {
			taskOperationError(c, err)
			return
		}
		common.SuccessResp(c)
	})
	if canRetry {
		g.POST("/retry_failed", func(c *gin.Context) {
			isAdmin, uid, ok := getUserInfo(c)
			if !ok {
				common.ErrorStrResp(c, "user invalid", 401)
				return
			}
			tasks := manager.GetByCondition(func(task T) bool {
				return canAccessTask(task, isAdmin, uid) && task.GetState() == tache.StateFailed
			})
			var failures []error
			for _, t := range tasks {
				if err := retryManagedTask(manager, t); err != nil {
					failures = append(failures, err)
				}
			}
			if err := errors.Join(failures...); err != nil {
				taskOperationError(c, err)
				return
			}
			common.SuccessResp(c)
		})
	}
}

func SetupTaskRoute(g *gin.RouterGroup) {
	taskRoute(g.Group("/upload"), fs.UploadTaskManager)
	taskRoute(g.Group("/copy"), fs.CopyTaskManager)
	taskRoute(g.Group("/move"), fs.MoveTaskManager)
	taskRoute(g.Group("/offline_download"), tool.DownloadTaskManager)
	taskRoute(g.Group("/offline_download_transfer"), tool.TransferTaskManager)
	taskRoute(g.Group("/decompress"), fs.ArchiveDownloadTaskManager)
	taskRoute(g.Group("/decompress_upload"), fs.ArchiveContentUploadTaskManager)
	// 种子相关任务：之前生成哈希的任务既没有进度也没有出现在任务列表里，
	// 秒传/中转保存则是纯同步请求。现在两者都能在任务列表看到进度。
	taskRoute(g.Group("/seed_generate"), fs.SeedGenerateTaskManager)
	taskRoute(g.Group("/seed_save"), fs.SeedSaveTaskManager, false)
}
