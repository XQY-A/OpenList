package handles

import (
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func saveAndInitOfflineDownloadTool(c *gin.Context, name string, items []model.SettingItem) (string, bool) {
	if err := op.SaveSettingItems(items); err != nil {
		common.ErrorResp(c, err, 500)
		return "", false
	}
	downloadTool, err := tool.Tools.Get(name)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return "", false
	}
	version, err := downloadTool.Init()
	if err != nil {
		common.ErrorResp(c, err, 500)
		return "", false
	}
	return version, true
}

func validateOfflineDownloadStorage(c *gin.Context, tempDir, nativeTool string) bool {
	if tempDir == "" {
		return true
	}
	storage, _, err := op.GetStorageAndActualPath(tempDir)
	if err != nil {
		common.ErrorStrResp(c, "storage does not exists", 400)
		return false
	}
	if storage.Config().CheckStatus && storage.GetStorage().Status != op.WORK {
		common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
		return false
	}
	if tool.NativeToolName(storage) != nativeTool {
		common.ErrorStrResp(c, "unsupported storage driver for offline download, only "+nativeTool+" is supported", 400)
		return false
	}
	return true
}

type SetAria2Req struct {
	Uri    string `json:"uri" form:"uri"`
	Secret string `json:"secret" form:"secret"`
}

func SetAria2(c *gin.Context) {
	var req SetAria2Req
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.Aria2Uri, Value: req.Uri, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.Aria2Secret, Value: req.Secret, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	version, ok := saveAndInitOfflineDownloadTool(c, "aria2", items)
	if !ok {
		return
	}
	common.SuccessResp(c, version)
}

type SetQbittorrentReq struct {
	Url      string `json:"url" form:"url"`
	Seedtime string `json:"seedtime" form:"seedtime"`
}

func SetQbittorrent(c *gin.Context) {
	var req SetQbittorrentReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.QbittorrentUrl, Value: req.Url, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.QbittorrentSeedtime, Value: req.Seedtime, Type: conf.TypeNumber, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "qBittorrent", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetTransmissionReq struct {
	Uri      string `json:"uri" form:"uri"`
	Seedtime string `json:"seedtime" form:"seedtime"`
}

func SetTransmission(c *gin.Context) {
	var req SetTransmissionReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	items := []model.SettingItem{
		{Key: conf.TransmissionUri, Value: req.Uri, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.TransmissionSeedtime, Value: req.Seedtime, Type: conf.TypeNumber, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "Transmission", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type Set115Req struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set115(c *gin.Context) {
	var req Set115Req
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "115 Cloud") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.Pan115TempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "115 Cloud", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type Set115OpenReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set115Open(c *gin.Context) {
	var req Set115OpenReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "115 Open") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.Pan115OpenTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "115 Open", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type Set123PanReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func Set123Pan(c *gin.Context) {
	var req Set123PanReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "123Pan") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.Pan123TempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "123Pan", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type Set123OpenReq struct {
	TempDir     string `json:"temp_dir" form:"temp_dir"`
	CallbackUrl string `json:"callback_url" form:"callback_url"`
}

func Set123Open(c *gin.Context) {
	var req Set123OpenReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "123 Open") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.Pan123OpenTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
		{Key: conf.Pan123OpenOfflineDownloadCallbackUrl, Value: req.CallbackUrl, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "123 Open", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetPikPakReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetPikPak(c *gin.Context) {
	var req SetPikPakReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "PikPak") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.PikPakTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "PikPak", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunder(c *gin.Context) {
	var req SetThunderReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "Thunder") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.ThunderTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "Thunder", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderXReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunderX(c *gin.Context) {
	var req SetThunderXReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "ThunderX") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.ThunderXTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "ThunderX", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetThunderBrowserReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetThunderBrowser(c *gin.Context) {
	var req SetThunderBrowserReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "ThunderBrowser") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.ThunderBrowserTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "ThunderBrowser", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

type SetGuangYaPanReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}

func SetGuangYaPan(c *gin.Context) {
	var req SetGuangYaPanReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if !validateOfflineDownloadStorage(c, req.TempDir, "GuangYaPan") {
		return
	}
	items := []model.SettingItem{
		{Key: conf.GuangYaPanTempDir, Value: req.TempDir, Type: conf.TypeString, Group: model.OFFLINE_DOWNLOAD, Flag: model.PRIVATE},
	}
	if _, ok := saveAndInitOfflineDownloadTool(c, "GuangYaPan", items); !ok {
		return
	}
	common.SuccessResp(c, "ok")
}

func OfflineDownloadTools(c *gin.Context) {
	tools := tool.Tools.NamesForPath(c.Query("path"))
	common.SuccessResp(c, tools)
}

type AddOfflineDownloadReq struct {
	Urls         []string `json:"urls"`
	Path         string   `json:"path"`
	Tool         string   `json:"tool"`
	DeletePolicy string   `json:"delete_policy"`
}

func AddOfflineDownload(c *gin.Context) {
	user, ok := c.Request.Context().Value(conf.UserKey).(*model.User)
	if !ok || user == nil {
		common.ErrorStrResp(c, "user invalid", 401)
		return
	}
	if !user.CanAddOfflineDownloadTasks() {
		common.ErrorStrResp(c, "permission denied", 403)
		return
	}

	var req AddOfflineDownloadReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	reqPath, err := user.JoinPath(req.Path)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	meta, err := op.GetNearestMeta(reqPath)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanWrite(user, meta, reqPath) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if req.Tool == tool.Open115Name {
		add115OfflineDownloads(c, reqPath, req)
		return
	}
	var tasks []task.TaskExtensionInfo
	for _, url := range req.Urls {
		// Filter out empty lines and whitespace-only strings
		trimmedUrl := strings.TrimSpace(url)
		if trimmedUrl == "" {
			continue
		}

		t, err := tool.AddURL(c, &tool.AddURLArgs{
			URL:          trimmedUrl,
			DstDirPath:   reqPath,
			Tool:         req.Tool,
			DeletePolicy: tool.DeletePolicy(req.DeletePolicy),
		})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if t != nil {
			tasks = append(tasks, t)
		}
	}
	common.SuccessResp(c, gin.H{
		"tasks": getTaskInfos(tasks),
	})
}

type addOfflineDownloadResult struct {
	Index  int    `json:"index"`
	TaskID string `json:"task_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

func add115OfflineDownloads(c *gin.Context, path string, req AddOfflineDownloadReq) {
	tasks := make([]task.TaskExtensionInfo, 0, len(req.Urls))
	results := make([]addOfflineDownloadResult, 0, len(req.Urls))
	hasURL := false
	var firstErr error
	for index, url := range req.Urls {
		result := addOfflineDownloadResult{Index: index}
		trimmedURL := strings.TrimSpace(url)
		if trimmedURL == "" {
			result.Error = "offline download URL is empty"
			results = append(results, result)
			continue
		}
		hasURL = true
		t, err := tool.AddURL(c, &tool.AddURLArgs{
			URL:          trimmedURL,
			DstDirPath:   path,
			Tool:         req.Tool,
			DeletePolicy: tool.DeletePolicy(req.DeletePolicy),
		})
		if err == nil && t == nil {
			err = errors.New("offline download task was not created")
		}
		if err != nil {
			result.Error = common.ErrorMessage(err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			result.TaskID = t.GetID()
			tasks = append(tasks, t)
		}
		results = append(results, result)
	}
	data := gin.H{"tasks": getTaskInfos(tasks), "results": results}
	if len(tasks) != 0 {
		common.SuccessResp(c, data)
		return
	}
	if !hasURL {
		common.ErrorWithDataResp(c, errors.New("no offline download URLs provided"), 400, data)
		return
	}
	common.ErrorWithDataResp(c, firstErr, 500, data)
}
