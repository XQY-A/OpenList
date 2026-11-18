package handles

import (
	"errors"
	"net/url"

	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
)

type taskDeleteResult struct {
	TaskID                string `json:"task_id"`
	LocalDeleted          bool   `json:"local_deleted"`
	CloudState            string `json:"cloud_state"`
	SourceFileDeleteState string `json:"source_file_delete_state"`
	Code                  int    `json:"code"`
	Reason                string `json:"reason,omitempty"`
	Error                 string `json:"error,omitempty"`
}

type taskDeleteOptions struct {
	deleteCloudRecord bool
	deleteSourceFile  bool
}

// An invalid or ambiguous option must not silently fall back to local removal.
func parseTaskDeleteOptions(c *gin.Context) (taskDeleteOptions, error) {
	var options taskDeleteOptions
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		return options, errors.New("invalid task deletion query")
	}
	for _, option := range []struct {
		name  string
		value *bool
	}{
		{"delete_cloud_record", &options.deleteCloudRecord},
		{"delete_source_file", &options.deleteSourceFile},
	} {
		values, exists := query[option.name]
		if !exists {
			continue
		}
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return options, errors.New(option.name + " must be a single true or false value")
		}
		*option.value = values[0] == "true"
	}
	if options.deleteSourceFile && !options.deleteCloudRecord {
		return options, errors.New("delete_source_file=true requires delete_cloud_record=true")
	}
	return options, nil
}

func taskOperationCode(err error) int {
	if errors.Is(err, tool.ErrTaskConflict) {
		return 409
	}
	return 500
}

func missingTaskDeleteResult(id string) taskDeleteResult {
	return taskDeleteResult{
		TaskID: id, CloudState: "none", SourceFileDeleteState: "none",
		Code: 404, Reason: "task_not_found", Error: "task not found",
	}
}

func deleteManagedCloudTask[T task.TaskExtensionInfo](c *gin.Context, manager task.Manager[T], t T, deleteSourceFile bool) taskDeleteResult {
	result := taskDeleteResult{TaskID: t.GetID(), CloudState: "none", SourceFileDeleteState: "none", Code: 409}
	download, ok := any(t).(*tool.DownloadTask)
	if !ok || !download.Is115Open() {
		result.Reason = "unsupported_tool"
		result.Error = "cloud record deletion is only supported for 115 Open download tasks"
		return result
	}
	cloud, err := download.DeleteCloudRecord(c.Request.Context(), deleteSourceFile)
	if cloud.CloudState != "" {
		result.CloudState = cloud.CloudState
	}
	result.SourceFileDeleteState = cloud.SourceFileDeleteState
	if err == nil && !cloud.LocalDeleted {
		err = errors.New("cloud cleanup did not complete local task deletion")
	}
	if err == nil && deleteSourceFile && cloud.SourceFileDeleteState != tool.SourceFileDeleteDeleted {
		err = errors.New("source file deletion was not confirmed")
	}
	if err != nil {
		result.Code = taskOperationCode(err)
		var cleanupErr *tool.CloudDeleteError
		if errors.As(err, &cleanupErr) {
			result.Reason = cleanupErr.Reason
		}
		result.Error = common.ErrorMessage(err)
		return result
	}
	manager.Remove(t.GetID())
	result.LocalDeleted = true
	result.Code = 200
	return result
}

func respondTaskDeletion(c *gin.Context, result taskDeleteResult) {
	if result.Code != 200 {
		common.ErrorWithDataResp(c, errors.New(result.Error), result.Code, result)
		return
	}
	common.SuccessResp(c, result)
}

func getDeleteTaskHandler[T task.TaskExtensionInfo](manager task.Manager[T]) gin.HandlerFunc {
	local := getTargetedHandler(manager, func(c *gin.Context, t T) {
		if err := removeManagedTask(manager, t); err != nil {
			taskOperationError(c, err)
			return
		}
		common.SuccessResp(c)
	})
	return func(c *gin.Context) {
		options, err := parseTaskDeleteOptions(c)
		if err != nil {
			common.ErrorResp(c, err, 400)
			return
		}
		if !options.deleteCloudRecord {
			local(c)
			return
		}
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		id := c.Query("tid")
		t, ok := manager.GetByID(id)
		if !ok || !canAccessTask(t, isAdmin, uid) {
			respondTaskDeletion(c, missingTaskDeleteResult(id))
			return
		}
		respondTaskDeletion(c, deleteManagedCloudTask(c, manager, t, options.deleteSourceFile))
	}
}

func getDeleteSomeTasksHandler[T task.TaskExtensionInfo](manager task.Manager[T]) gin.HandlerFunc {
	local := getBatchHandler(manager, func(t T) error { return removeManagedTask(manager, t) })
	return func(c *gin.Context) {
		options, err := parseTaskDeleteOptions(c)
		if err != nil {
			common.ErrorResp(c, err, 400)
			return
		}
		if !options.deleteCloudRecord {
			local(c)
			return
		}
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		var ids []string
		if err := c.ShouldBind(&ids); err != nil {
			common.ErrorStrResp(c, "invalid request format", 400)
			return
		}
		results := make([]taskDeleteResult, 0, len(ids))
		seen := make(map[string]taskDeleteResult, len(ids))
		for _, id := range ids {
			if result, exists := seen[id]; exists {
				results = append(results, result)
				continue
			}
			result := missingTaskDeleteResult(id)
			if t, ok := manager.GetByID(id); ok && canAccessTask(t, isAdmin, uid) {
				result = deleteManagedCloudTask(c, manager, t, options.deleteSourceFile)
			}
			seen[id] = result
			results = append(results, result)
		}
		common.SuccessResp(c, gin.H{"results": results})
	}
}

func getClearTasksHandler[T task.TaskExtensionInfo](manager task.Manager[T], states ...tache.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		options, err := parseTaskDeleteOptions(c)
		if err != nil {
			common.ErrorResp(c, err, 400)
			return
		}
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		tasks := manager.GetByCondition(func(t T) bool {
			return canAccessTask(t, isAdmin, uid) && argsContains(t.GetState(), states...)
		})
		if options.deleteCloudRecord {
			results := make([]taskDeleteResult, 0, len(tasks))
			for _, t := range tasks {
				results = append(results, deleteManagedCloudTask(c, manager, t, options.deleteSourceFile))
			}
			common.SuccessResp(c, gin.H{"results": results})
			return
		}
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
	}
}
