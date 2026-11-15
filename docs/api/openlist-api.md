# OpenList custom/115 API 完整参考（供 AI 阅读）

本文面向编写 OpenList 客户端、脚本和集成的 AI。接口依据本仓库 `custom/115` 分支实际源码；包含原有 OpenList JSON API 和个人 115 Open 扩展。源码分析基线为 `84f0d064c6659f389e39c7c37adbd84dfd9ce768`，后续是否需要重新生成以文档中的源码摘要为准。

## 阅读方式与优先级

- `openlist-api.md` 是可直接上传或让 AI 读取的单文件参考，包含本文、115 扩展合同、全部 `/api/` 路由、请求结构、响应构造和源码依据。
- `openlist.openapi.json` 是同一份接口的 OpenAPI 3.1 文件，展开标准可表示的方法，CONNECT／TRACE 保留在扩展元数据中；适合检索、导入接口工具或生成客户端基础代码。
- 在线官方参考为 <https://fox.oplist.org/>，AI 索引为 <https://fox.oplist.org/llms.txt>。官方参考可能滞后，也不包含个人 115 扩展。发生冲突时，以本分支源码及本文的合同为准。
- 路由、请求字段和必填标记从 Go AST 与实际 JSON/form/binding 标签提取；动态响应、驱动相关数据和第三方协议不伪造固定结构。OpenAPI 中开放的 `data` schema 配有 `x-openlist-contract`，可在顶层 `x-openlist-contracts` 查到实际处理函数、后续调用和响应构造源码。生成器不会把未核实的数据填成固定样例。
- 完整范围是当前常规运行模式的 `/api/` 接口，包括任务类型展开和兼容的管理员任务路径。下载、WebDAV、S3、MCP、Emby/OpenSubsonic 等协议入口在下文另列；调试专用接口、静态网页资源、FTP/SFTP 命令不属于 JSON API 清单。

## 地址、认证与响应

`BASE_URL` 是实例的外部地址，例如 `https://files.example.com`。实例配置了站点子路径时，包含该子路径；下面的 `/api/...` 均拼接在 `BASE_URL` 后。不要把挂载路径当作操作系统路径，也不要把存储驱动的 access token 当作 OpenList 登录 token。

普通 API 的请求头：

```http
Authorization: <OpenList JWT 或管理设置中的 API token>
Content-Type: application/json
```

普通 OpenList API 的 `Authorization` **直接填写 token，不加 `Bearer ` 前缀**。空 token 作为访客处理；访客是否可用由实例设置及具体接口决定。`/api/task` 与 `/api/share` 不允许访客；`/api/admin` 只允许管理员。文件操作还校验用户权限、用户根目录和路径元信息。

普通 JSON API 使用以下响应信封：

```json
{"code":200,"message":"success","data":null}
```

`data` 可以是对象、数组、字符串或 `null`。**业务失败通常仍返回 HTTP 200**，必须检查 JSON `code`。400 为请求问题，401 为认证问题，403 为权限问题，404 为不可见或不存在的对象，409 为状态冲突，500 为服务／驱动／持久化问题；具体接口还可使用其他业务码。登录需要 2FA 时会返回业务码 402，登录限流可返回 429。响应源码中 `common.ErrorResp`、`ErrorStrResp`、`ErrorWithDataResp` 的数字是业务码，不是实际 HTTP 状态。

文件字节、重定向、SSO/WebAuthn、媒体导出和转码切片等特殊响应要按接口源码处理。例如 Worker 切片上传成功使用 HTTP 204，无 JSON 信封。不能把所有接口一律按 HTTP 200 JSON 解析。

GET 请求使用查询参数。注册为 `Any` 的接口接受 Gin 的 GET、POST、PUT、PATCH、HEAD、OPTIONS、DELETE、CONNECT、TRACE 方法；一般客户端优先用 **POST + JSON** 调用文件列出／获取／搜索等接口。GET 的字段名称按 `form` 标签或 Go 默认字段名绑定，不应假定它始终与 JSON 名称相同。明确调用 `ShouldBindJSON` 的接口只按 JSON 请求体绑定。

## 登录与最小调用示例

```http
POST /api/auth/login
```

```json
{"username":"<用户名>","password":"<原始密码>","otp_code":"<启用2FA时填写>"}
```

成功后保存 `data.token`，后续放入 `Authorization`。`POST /api/auth/login/hash` 的 `password` 为原始密码加 `-https://github.com/alist-org/alist` 后进行 SHA-256 的小写十六进制结果；此历史盐值仍是当前源码使用的值。LDAP 登录使用 `/api/auth/login/ldap` 和原始 LDAP 密码。token 的有效期由实例配置决定，不假定固定为 48 小时。注销使用 `GET /api/auth/logout`。

```http
POST /api/fs/list
```

```json
{"path":"/115/Downloads","password":"","page":1,"per_page":100,"refresh":false}
```

成功响应的 `data` 包含 `content`、`total` 以及目录展示信息。文件获取使用 `POST /api/fs/get`，请求 `{"path":"/115/Downloads/example.mp4","password":""}`；读取 `data.raw_url`、`sign` 等实际返回字段。不要假定所有驱动的 `raw_url` 都可无需附加头直接访问。

普通分页使用 `page` 和 `per_page`；调用 `PageReq.Validate()` 的接口将小于 1 的页码调整为 1，小于 1 的 `per_page` 调整为不限制。媒体库等接口使用自己的 `page_size`，以各接口字段为准。任务列表没有统一分页参数。

## 通用文件、存储与任务规则

- JSON 文件路径按当前用户可见路径传入；后台挂载管理使用 OpenList 的实际挂载路径。分享读取由 `/api/fs/list`、`/api/fs/get`、压缩包读取的 split handler 识别 `/@s` 路径，不额外创造 `/api/share/fs/list`。
- `mkdir` 使用 `path`；`rename` 使用 `path` 和 `name`；移动／复制使用 `src_dir`、`dst_dir`、`names` 和可选 `conflict_policy`、`follow_seed`；删除文件使用 `dir`、`names` 和可选 `follow_seed`。具体结构在全量清单的请求 schema 中。
- `PUT /api/fs/put` 的请求体是文件字节；`PUT /api/fs/form` 使用 multipart/form-data，文件字段为 `file`。`File-Path` 头填写 URL 编码后的完整目标文件路径；`As-Task: true` 请求异步上传；`Overwrite: false` 禁止覆盖，默认允许覆盖。可提供 `X-File-Size`、`X-File-Md5`、`X-File-Sha1`、`X-File-Sha256`、`Last-Modified` 等头，支持情况以 handler 为准。
- 分块上传依次使用 `/api/fs/multipart/init`、`chunk`、`complete`；会话头 `X-Upload-Id`，分块序号头 `X-Chunk-Index`。初始化还使用 `File-Path`、`X-File-Size` 和可选 `X-Chunk-Size`；`status` 查询会话，`abort` 中止。客户端直传另用 `/api/fs/get_direct_upload_info`，其返回能力由驱动决定。
- 创建／修改挂载的 `addition` 是驱动配置的 JSON **字符串**。先从 `/api/admin/driver/info?driver=<驱动名>` 获取该驱动的字段定义，不为所有驱动构造同一个 `addition` 对象。
- 存储 `mount_details` 的磁盘容量字段按自定义 JSON 序列化返回 `total_space`、`used_space`、`free_space`，单位字节；不是 Go 字段名 `TotalSpace`／`UsedSpace`。`free_space` 为前两者之差。
- 通用设置的写入请求是 `SettingItem` 数组；某些工具专用设置另有 `/api/admin/setting/set_*` 入口。任务删除选项不等于工具设置，也不等于下载请求的 `delete_policy`。

### 任务路径与执行状态

每种任务在 `/api/task/<类型>` 下提供 `undone`、`done`、`info`、`cancel`、`cancel_some`、`delete`、`delete_some`、`clear_done`、`clear_succeeded`，可重试的类型还提供 `retry`、`retry_some`、`retry_failed`。兼容管理员路径为 `/api/admin/task/<类型>`，不是 `/api/admin/task` 下的无类型通用端点。全部实际类型和路径见生成的清单；`seed_save` 不注册三个 retry 接口。

单任务操作使用查询参数 `tid=<本地任务ID>`；批量操作的 JSON 请求体直接是 ID 字符串数组：`["<ID1>","<ID2>"]`，不是 `{"tids":[...]}`。列表查询是 GET，其他任务操作是 POST。任务由创建者和管理员访问；不可见 ID 与不存在 ID 均按 404 处理。

通用 `TaskInfo` 包含 `id`、`name`、`creator`、`creator_role`、`state`、`status`、`progress`、`start_time`、`end_time`、`total_bytes`、`error`。115 Open 下载任务另外包含下节的扩展字段；其他任务不会因为字段缺失而表示失败。

| `state` | 含义 | 所属列表 |
| --- | --- | --- |
| 0 | Pending，等待执行 | undone |
| 1 | Running，正在执行 | undone |
| 2 | Succeeded，成功 | done |
| 3 | Canceling，正在取消 | undone |
| 4 | Canceled，已取消 | done |
| 5 | Errored，发生错误、仍可自动重试 | undone |
| 6 | Failing，正在执行失败处理 | undone |
| 7 | Failed，已失败 | done |
| 8 | WaitingRetry，等待重试 | undone |
| 9 | BeforeRetry，准备重试 | undone |

本地执行成功不一定表示跨存储转存结束，需继续检查 `offline_download_transfer`。取消、删除本地任务、删除云端记录是不同操作。取消的副作用取决于工具，115 Open 的运行中取消可能调用云端移除；不要用取消替代只删除本地展示记录。

通用批量 `cancel_some`、`retry_some` 及仅本地 `delete_some` 返回 `data` 为 `{任务ID: 错误信息}` 的失败映射，空对象表示无逐项错误；顶层业务码为 200。115 Open **可选云删除**使用不同的逐项结果结构，下节完整说明。`retry_failed`／仅本地 clear 若内部操作失败可返回业务码 500 或冲突码；不要仅以请求发出或列表暂时变化判断所有目标已处理。

### 安全使用 115 扩展的客户端流程

1. 调用 `/api/public/offline_download_tools?path=<用户可见目标路径>`，核对可用工具；添加时明确设置 `tool: "115 Open"`。
2. 逐项保存添加结果的 `index` 和 `task_id`，成功项不重新提交。异步受理时使用同一本地 ID 查询；可采用约 5 秒串行轮询，上一请求完成后再发下一请求。
3. `submission_state=accepted` 且 `info_hash` 非空表示持有 115 回执；执行是否结束仍由 `state` 决定。`unknown` 或丢失添加响应时先核对已有任务与原账号。
4. 默认删除只移除本地记录。明确需要清理云记录时传一次字面值 `delete_cloud_record=true`，逐项保存回执，按 `local_deleted` 和业务 `code` 判断结果。
5. 清理失败保留同一 ID。未知清理的重试只核对不存在，不重新发送删除；不以重新添加相同下载来修复清理状态。具体状态与原因以下节为准。

## 其他 HTTP／协议入口

| 入口 | 作用与调用约定 |
| --- | --- |
| `/ping` | Any，响应 HTTP 200 纯文本 `pong` |
| `/d/*path`、`/p/*path` | GET／HEAD，下载重定向与代理，按签名及下载规则处理；`sign` 使用文件查询返回值 |
| `/ad/*path`、`/ap/*path`、`/ae/*path` | GET／HEAD，压缩包下载、代理与内部文件提取；使用压缩包签名和具体查询参数 |
| `/sd/:sid`、`/sd/:sid/*path`、`/sad/:sid`、`/sad/:sid/*path` | GET／HEAD，分享下载及压缩包提取；分享密码参数 `pwd`，压缩包内部路径 `inner`、密码 `pass` 等按对应 handler |
| `/dav`、`/dav/*path` | WebDAV 协议，包括 PROPFIND、MKCOL、LOCK、UNLOCK、PROPPATCH、COPY、MOVE；使用 HTTP Basic 等 WebDAV 认证，不使用普通 JSON 响应信封 |
| `/s3/*path` | S3 兼容协议，受配置开关和 S3 认证控制 |
| `/mcp` | 启用时提供 MCP Streamable HTTP 的 GET／POST／DELETE；禁用时返回业务码 403。工具为 `openlist.fs.list`、`openlist.fs.get`、`openlist.fs.link`，具体 JSON-RPC 协议以 `server/mcp` 实现为准 |
| `/@emby/*path`、`/@mediabrowser/*path` | Emby 兼容路由，路径大小写归一化，自带认证与功能开关 |
| `/@rest/*path` | OpenSubsonic 兼容路由，自带认证与功能开关 |
| `/tc/:job/:token/master.m3u8`、`/tc/:job/:token/:profile/playlist.m3u8`、`/tc/:job/:token/:profile/:seg` | GET，HLS 转码播放，路径 token 是播放能力凭据 |

转码 Worker 的注册／心跳／领取任务使用共享 worker secret，可接受原始 secret 或 `Bearer <secret>`。`/api/transcode/worker/segment` 和 `/job/finish` 使用该 job 的 `Authorization: Bearer <callback_token>`。这两种 token 与普通 OpenList JWT 区别明确，不能互换。

媒体库／转码／扫描／种子接口的请求和响应结构包含在下文全量清单中。媒体数据字段可能随媒体类型、扫描和刮削结果变化；驱动能力也应由 capabilities／driver info 等实际响应获取，不凭工具名称推断支持全部操作。

## 维护与验证

在仓库根目录执行 `python3 scripts/export_api_docs.py` 更新两个生成文件；执行 `python3 scripts/export_api_docs.py --check` 检查文档是否与当前源码一致。生成过程仅读取 Go 语法及文档，不启动服务、不连接真实 115 账号、不执行下载或删除。文档一致性检查不等同于真实云端全流程验收；115 使用说明中的真实验收边界仍适用。

源码摘要覆盖提取器扫描的非测试 Go 源文件及 `go.mod`／`go.sum`，可发现请求模型和底层行为的变化。中间件清单包含条件启用的中间件，是否启用仍取决于实例配置。

生成器列出源码明确注册的路由及请求字段，并保留动态响应的实际构造表达式与处理函数。新增复杂协议、条件绑定或自定义 JSON 序列化时，需要结合源码人工补充合同；不能把开放 schema 理解为任何参数或结果都合法。


## 115 Open 定制接口完整合同

### 115 Open offline download API

For the complete API reference for this branch, including all other OpenList
endpoints, see [the AI reference](openlist-api.md) and
[OpenAPI 3.1](openlist.openapi.json).

The `115 Open` tool keeps the original `info_hash` returned by 115, together
with the storage and account that submitted the task. The receipt is persisted
even when general download task persistence is disabled. Treat the hash as an
opaque string; it is not necessarily a BTIH or an ed2k file hash.

See the [usage and upgrade guide](../usage/115-open-offline.md) for the management
interface, transfer policies and deployment steps, and the
[personal build guide](../development/custom-115-build.md) for paired frontend
and backend builds.

These endpoints use the usual OpenList authentication and response envelope.
Check the JSON `code`: a business error is still returned with HTTP 200.
Only the task creator and administrators can query a task. Other users receive
the same 404 response as an unknown task ID.

## Add downloads

`POST /api/fs/add_offline_download`

```json
{
  "urls": ["<download URI>", "<another download URI>"],
  "path": "/115/Downloads",
  "tool": "115 Open"
}
```

Adding downloads remains asynchronous. A successful item means its local task
has been saved and queued, not that 115 has already accepted the download.
The response keeps the existing `data.tasks` array and adds `data.results` for
requests that explicitly select `115 Open`:

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "tasks": [
      {
        "id": "<local task ID>",
        "tool": "115 Open",
        "info_hash": "",
        "submission_state": "pending"
      }
    ],
    "results": [
      {"index": 0, "task_id": "<local task ID>"},
      {"index": 1, "error": "<reason the second task could not be created>"}
    ]
  }
}
```

The example omits unchanged task fields. Each result's zero-based `index`
refers to the original `urls` array, including empty or whitespace-only items.
All successfully created tasks remain in `tasks`, even if another item fails.
At least one created task yields code 200; inspect every result for partial
failure. An empty batch or a batch containing only blank items yields code 400.
If every nonblank item fails to create, code 500 includes the per-item results.
Request-wide permission, path or decoding errors retain the usual error format.
Other tools retain their existing batch response and behavior.

Retry only the failed inputs after addressing their errors. Do not resubmit
successful items to obtain their hash.

## Read the receipt

Use the returned local task ID with any of these endpoints:

| Endpoint | Result |
| --- | --- |
| `POST /api/task/offline_download/info?tid=<local task ID>` | One task |
| `GET /api/task/offline_download/undone` | Tasks that have not ended |
| `GET /api/task/offline_download/done` | Ended tasks, including restored tasks |

Every 115 Open task has these additional fields. Other task types may omit them.

| Field | Meaning |
| --- | --- |
| `tool` | `115 Open` |
| `info_hash` | The exact cloud receipt, or an empty string while unavailable |
| `submission_state` | Submission outcome, separate from download execution `state` |

The submission states are:

| Value | Meaning |
| --- | --- |
| `pending` | Saved locally; no submission has started |
| `submitting` | Submission intent and account binding were saved; the outcome is pending |
| `accepted` | A cloud receipt is known; subsequent execution queries that same receipt |
| `failed` | Preparation failed or the request was authoritatively rejected |
| `unknown` | The request may have been accepted, but no authoritative receipt is available |
| `legacy` | An older task lacks a reliable saved receipt or account binding |

The first add response may already contain a hash if acceptance was fast.
Otherwise, query the same task ID until the state changes. A later task response
contains `"submission_state": "accepted"` and the original, nonempty `info_hash`.
An accepted receipt does not by itself mean the download completed; continue
to use the existing execution state, progress and error fields.

Restarts and explicit retries preserve a known receipt and query the original
cloud task instead of submitting it again. A receipt whose database write failed
remains visible in memory with a persistence error; retrying first attempts to
save it. A crash before that write succeeds cannot recover the receipt, so the
last durable `submitting` state becomes `unknown`. Tasks with `unknown` or
`legacy` state are not automatically resubmitted and cannot bypass this rule
through the retry endpoints. Verify the cloud account before intentionally
creating a new download.

## Optionally remove the cloud download record

The existing deletion endpoints accept `delete_cloud_record=true`:

| Endpoint | Targets |
| --- | --- |
| `POST /api/task/offline_download/delete?tid=<ID>&delete_cloud_record=true` | One task |
| `POST /api/task/offline_download/delete_some?delete_cloud_record=true` | JSON array of task IDs |
| `POST /api/task/offline_download/clear_done?delete_cloud_record=true` | All ended tasks accessible to the caller |
| `POST /api/task/offline_download/clear_succeeded?delete_cloud_record=true` | All successful tasks accessible to the caller |

Omitting the option, or passing `false`, preserves the existing local-only
deletion and response format. Local deletion does not cancel a running download.
The saved deletion marker prevents it from reappearing after a restart. If that
marker cannot be saved, deletion reports an error and retains the task.

The option must occur once with the literal value `true` or `false`. Empty,
duplicate, malformed or other values return code 400 without deleting anything.
It is separate from `delete_policy`, which controls the existing transfer and
temporary-file behavior.

When `true`, the operation applies only to `115 Open` download tasks. Other
tools, `115 Cloud`, and `offline_download_transfer` tasks return a conflict and
remain in the list. A task must have a reliable cloud receipt and its original
storage/account binding, and its local state must be succeeded, failed or
canceled. Use the existing cancel action for an active local task.

Before cloud deletion, the server checks the current bound account and queries
the cloud task. Only cloud status `2` (completed), `-1` (failed), or a reliable
complete lookup proving absence is accepted. A currently running cloud task,
unknown status, account change or query failure leaves the local task intact.
Cloud requests and waits for task/account coordination are bounded by a timeout.

Cloud deletion always uses `del_source_file=0`: it removes the 115 download
record and **preserves downloaded files**. There is no new delete-files option.
Existing transfer policies still apply independently to their files.

The server saves cloud deletion intent before the first lookup, confirms and
saves the cloud result, then saves the local deletion marker and removes the
list entry. Saving intent before lookup also protects a confirmed absence whose
later database write fails. A first attempt that proves no deletion was sent
can reset its intent after a failed query or an ineligible cloud state, provided
that reset is saved successfully.

An uncertain deletion remains queryable. Explicitly retrying `deleting` or
`unknown` cleanup only checks the cloud record: reliable absence completes local
cleanup, while any existing record returns code 409 with reason
`cloud_delete_unknown` without sending another delete. The API cannot determine
whether that hash belongs to the original record or a newly created download.
When a confirmed `deleted` record appears again, the reason is
`cloud_record_reappeared`. These protections also apply after a database failure
and process restart. Inspect the original cloud account before further action;
local-only deletion is still available.

The 115 API has no task generation or conditional-delete parameter. Coordination
covers this OpenList process; another client can recreate the same hash between
the cloud query and deletion. This is not an atomic check-and-delete across
clients.

### Deletion results

A single `true` request returns one result in `data`. A batch or clear request
returns `data.results`, with the same structure for every target:

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "results": [
      {
        "task_id": "<removed task ID>",
        "local_deleted": true,
        "cloud_state": "deleted",
        "code": 200
      },
      {
        "task_id": "<retained task ID>",
        "local_deleted": false,
        "cloud_state": "none",
        "code": 409,
        "reason": "cloud_task_active",
        "error": "<explanation>"
      }
    ]
  }
}
```

Batch and clear responses use top-level code 200 even when some or all targets
fail; inspect each result's `code` and `local_deleted`. Single-item errors use
the same error code at the top level and in the result. Permission failures and
unknown task IDs both produce 404; state, type and binding conflicts use 409;
cloud request or persistence failures use 500. `reason` is a machine-readable
error category when available, and `error` is filtered using the existing
privacy rules. Duplicate IDs within a `true` batch reuse their first result
without sending another delete request. Failure never silently falls back to
local-only deletion.

`cloud_state` records the last known cleanup outcome:

| Value | Meaning |
| --- | --- |
| `none` | No cloud deletion has been sent |
| `deleting` | Deletion intent has been saved |
| `unknown` | The previous attempt is uncertain; retries only reconcile confirmed absence |
| `deleted` | Deletion or absence was confirmed; local cleanup may still need to finish |

A failed local write can therefore return `cloud_state: "deleted"` together
with `local_deleted: false`. Keep that task ID and retry its cleanup rather than
creating another download. On restart, interrupted `deleting` becomes `unknown`;
cleanup is never automatically sent during recovery.

### Advertised availability

115 Open task responses also include:

| Field | Meaning |
| --- | --- |
| `can_delete_cloud_record` | Whether the local task and current storage binding allow a cloud-cleanup attempt |
| `cloud_delete_unavailable_reason` | An availability reason code, empty when an attempt is allowed |
| `cloud_delete_state` | The saved or currently known cleanup state |

Computing availability does not query 115. A task may advertise availability
while its cloud counterpart is still active; the deletion request checks again
and returns a conflict. Clients must tolerate missing fields when connected to
an older backend, and should keep the cloud-cleanup option unchecked by default.


## 当前源码全量接口清单

源码摘要：`f7e9a474e2b3e4e4b011ff4606fc689542fe6376176c9f2a92463f30f53b274f`。共 398 个方法／路径注册项（Gin Any 按一个注册项计），OpenAPI 展开为 470 个标准方法操作。

`Any` 推荐按 POST JSON 调用；OpenAPI 展开其七种可表示的标准方法，额外 CONNECT／TRACE 仅在 Gin Any 中支持。下面的请求结构名称可在后文查找。所有条目均来自当前源码，不含官方文档里未在当前分支注册的路径。

| 方法 | 路径 | 请求结构／处理函数 |
| --- | --- | --- |
| GET | `/api/admin/driver/info` | `server/handles.GetDriverInfo` |
| GET | `/api/admin/driver/list` | `server/handles.ListDriverInfo` |
| GET | `/api/admin/driver/names` | `server/handles.ListDriverNames` |
| POST | `/api/admin/index/build` | `server/handles.BuildIndex` |
| POST | `/api/admin/index/clear` | `server/handles.ClearIndex` |
| GET | `/api/admin/index/progress` | `server/handles.GetProgress` |
| POST | `/api/admin/index/stop` | `server/handles.StopIndex` |
| POST | `/api/admin/index/update` | `server/handles.UpdateIndexReq` |
| GET | `/api/admin/media/album/items` | `server/handles.ListMediaAlbumItemsAdmin` |
| POST | `/api/admin/media/album/update` | `server/handles.UpdateMediaAlbumReq` |
| GET | `/api/admin/media/albums` | `server/handles.ListMediaAlbumsAdmin` |
| POST | `/api/admin/media/appkey/create` | `server/handles.CreateMediaAppKey.req` |
| POST | `/api/admin/media/appkey/delete` | `server/handles.DeleteMediaAppKey` |
| GET | `/api/admin/media/appkey/list` | `server/handles.ListMediaAppKeys` |
| POST | `/api/admin/media/clear` | `server/handles.ClearMediaDB` |
| POST | `/api/admin/media/clear_scrape` | `server/handles.ClearMediaScrape` |
| GET | `/api/admin/media/config/list` | `server/handles.ListMediaConfigs` |
| POST | `/api/admin/media/config/save` | `server/handles.SaveMediaConfigReq` |
| POST | `/api/admin/media/delete_invalid` | `server/handles.DeleteInvalidMedia` |
| GET | `/api/admin/media/export` | `server/handles.ExportMediaDB` |
| POST | `/api/admin/media/import` | `server/handles.ImportMediaDB` |
| GET | `/api/admin/media/items` | `server/handles.ListMediaItemsAdmin` |
| POST | `/api/admin/media/items/cover` | `server/handles.UploadMediaItemCover` |
| POST | `/api/admin/media/items/delete` | `server/handles.DeleteMediaItemAdmin` |
| POST | `/api/admin/media/items/update` | `server/handles.UpdateMediaItemReq` |
| GET | `/api/admin/media/scan/progress` | `server/handles.GetMediaScanProgress` |
| POST | `/api/admin/media/scan/start` | `server/handles.ScanMediaReq` |
| GET | `/api/admin/media/scan_paths` | `server/handles.ListMediaScanPaths` |
| POST | `/api/admin/media/scan_paths/clear` | `server/handles.ClearMediaScanPathDB` |
| POST | `/api/admin/media/scan_paths/create` | `server/handles.CreateMediaScanPathReq` |
| POST | `/api/admin/media/scan_paths/delete` | `server/handles.DeleteMediaScanPath` |
| POST | `/api/admin/media/scan_paths/update` | `server/handles.UpdateMediaScanPathReq` |
| POST | `/api/admin/media/scrape/start` | `server/handles.ScrapeMediaReq` |
| GET | `/api/admin/media/scrape/stats` | `server/handles.GetMediaScrapeStats` |
| POST | `/api/admin/media/strm/generate` | `server/handles.GenerateMediaStrm.req` |
| POST | `/api/admin/message/get` | `internal/message.GetHandle` |
| POST | `/api/admin/message/send` | `internal/message.Req` |
| POST | `/api/admin/meta/create` | `internal/model.Meta` |
| POST | `/api/admin/meta/delete` | `server/handles.DeleteMeta` |
| GET | `/api/admin/meta/get` | `server/handles.GetMeta` |
| GET | `/api/admin/meta/list` | `internal/model.PageReq` |
| POST | `/api/admin/meta/update` | `internal/model.Meta` |
| GET | `/api/admin/scan/progress` | `server/handles.GetManualScanProgress` |
| POST | `/api/admin/scan/start` | `server/handles.ManualScanReq` |
| POST | `/api/admin/scan/stop` | `server/handles.StopManualScan` |
| POST | `/api/admin/setting/default` | `server/handles.DefaultSettings` |
| POST | `/api/admin/setting/delete` | `server/handles.DeleteSetting` |
| GET | `/api/admin/setting/get` | `server/handles.GetSetting` |
| GET | `/api/admin/setting/list` | `server/handles.ListSettings` |
| POST | `/api/admin/setting/reset_token` | `server/handles.ResetToken` |
| POST | `/api/admin/setting/save` | `[]internal/model.SettingItem` |
| POST | `/api/admin/setting/set_115` | `server/handles.Set115Req` |
| POST | `/api/admin/setting/set_115_open` | `server/handles.Set115OpenReq` |
| POST | `/api/admin/setting/set_123_open` | `server/handles.Set123OpenReq` |
| POST | `/api/admin/setting/set_123_pan` | `server/handles.Set123PanReq` |
| POST | `/api/admin/setting/set_aria2` | `server/handles.SetAria2Req` |
| POST | `/api/admin/setting/set_guangyapan` | `server/handles.SetGuangYaPanReq` |
| POST | `/api/admin/setting/set_pikpak` | `server/handles.SetPikPakReq` |
| POST | `/api/admin/setting/set_qbit` | `server/handles.SetQbittorrentReq` |
| POST | `/api/admin/setting/set_thunder` | `server/handles.SetThunderReq` |
| POST | `/api/admin/setting/set_thunder_browser` | `server/handles.SetThunderBrowserReq` |
| POST | `/api/admin/setting/set_thunderx` | `server/handles.SetThunderXReq` |
| POST | `/api/admin/setting/set_transmission` | `server/handles.SetTransmissionReq` |
| POST | `/api/admin/storage/create` | `internal/model.Storage` |
| POST | `/api/admin/storage/delete` | `server/handles.DeleteStorage` |
| POST | `/api/admin/storage/disable` | `server/handles.DisableStorage` |
| POST | `/api/admin/storage/enable` | `server/handles.EnableStorage` |
| GET | `/api/admin/storage/get` | `server/handles.GetStorage` |
| GET | `/api/admin/storage/list` | `internal/model.PageReq` |
| POST | `/api/admin/storage/load_all` | `server/handles.LoadAllStorages` |
| POST | `/api/admin/storage/update` | `internal/model.Storage` |
| POST | `/api/admin/task/copy/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/copy/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/copy/clear_done` | `无请求体` |
| POST | `/api/admin/task/copy/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/copy/delete` | `tid 查询参数` |
| POST | `/api/admin/task/copy/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/copy/done` | `无请求体` |
| POST | `/api/admin/task/copy/info` | `tid 查询参数` |
| POST | `/api/admin/task/copy/retry` | `tid 查询参数` |
| POST | `/api/admin/task/copy/retry_failed` | `无请求体` |
| POST | `/api/admin/task/copy/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/copy/undone` | `无请求体` |
| POST | `/api/admin/task/decompress/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/decompress/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/decompress/clear_done` | `无请求体` |
| POST | `/api/admin/task/decompress/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/decompress/delete` | `tid 查询参数` |
| POST | `/api/admin/task/decompress/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/decompress/done` | `无请求体` |
| POST | `/api/admin/task/decompress/info` | `tid 查询参数` |
| POST | `/api/admin/task/decompress/retry` | `tid 查询参数` |
| POST | `/api/admin/task/decompress/retry_failed` | `无请求体` |
| POST | `/api/admin/task/decompress/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/decompress/undone` | `无请求体` |
| POST | `/api/admin/task/decompress_upload/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/decompress_upload/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/decompress_upload/clear_done` | `无请求体` |
| POST | `/api/admin/task/decompress_upload/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/decompress_upload/delete` | `tid 查询参数` |
| POST | `/api/admin/task/decompress_upload/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/decompress_upload/done` | `无请求体` |
| POST | `/api/admin/task/decompress_upload/info` | `tid 查询参数` |
| POST | `/api/admin/task/decompress_upload/retry` | `tid 查询参数` |
| POST | `/api/admin/task/decompress_upload/retry_failed` | `无请求体` |
| POST | `/api/admin/task/decompress_upload/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/decompress_upload/undone` | `无请求体` |
| POST | `/api/admin/task/move/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/move/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/move/clear_done` | `无请求体` |
| POST | `/api/admin/task/move/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/move/delete` | `tid 查询参数` |
| POST | `/api/admin/task/move/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/move/done` | `无请求体` |
| POST | `/api/admin/task/move/info` | `tid 查询参数` |
| POST | `/api/admin/task/move/retry` | `tid 查询参数` |
| POST | `/api/admin/task/move/retry_failed` | `无请求体` |
| POST | `/api/admin/task/move/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/move/undone` | `无请求体` |
| POST | `/api/admin/task/offline_download/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/offline_download/clear_done` | `无请求体` |
| POST | `/api/admin/task/offline_download/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/offline_download/delete` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/offline_download/done` | `无请求体` |
| POST | `/api/admin/task/offline_download/info` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download/retry` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download/retry_failed` | `无请求体` |
| POST | `/api/admin/task/offline_download/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/offline_download/undone` | `无请求体` |
| POST | `/api/admin/task/offline_download_transfer/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download_transfer/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/offline_download_transfer/clear_done` | `无请求体` |
| POST | `/api/admin/task/offline_download_transfer/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/offline_download_transfer/delete` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download_transfer/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/offline_download_transfer/done` | `无请求体` |
| POST | `/api/admin/task/offline_download_transfer/info` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download_transfer/retry` | `tid 查询参数` |
| POST | `/api/admin/task/offline_download_transfer/retry_failed` | `无请求体` |
| POST | `/api/admin/task/offline_download_transfer/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/offline_download_transfer/undone` | `无请求体` |
| POST | `/api/admin/task/seed_generate/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/seed_generate/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/seed_generate/clear_done` | `无请求体` |
| POST | `/api/admin/task/seed_generate/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/seed_generate/delete` | `tid 查询参数` |
| POST | `/api/admin/task/seed_generate/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/seed_generate/done` | `无请求体` |
| POST | `/api/admin/task/seed_generate/info` | `tid 查询参数` |
| POST | `/api/admin/task/seed_generate/retry` | `tid 查询参数` |
| POST | `/api/admin/task/seed_generate/retry_failed` | `无请求体` |
| POST | `/api/admin/task/seed_generate/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/seed_generate/undone` | `无请求体` |
| POST | `/api/admin/task/seed_save/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/seed_save/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/seed_save/clear_done` | `无请求体` |
| POST | `/api/admin/task/seed_save/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/seed_save/delete` | `tid 查询参数` |
| POST | `/api/admin/task/seed_save/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/seed_save/done` | `无请求体` |
| POST | `/api/admin/task/seed_save/info` | `tid 查询参数` |
| GET | `/api/admin/task/seed_save/undone` | `无请求体` |
| POST | `/api/admin/task/upload/cancel` | `tid 查询参数` |
| POST | `/api/admin/task/upload/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/admin/task/upload/clear_done` | `无请求体` |
| POST | `/api/admin/task/upload/clear_succeeded` | `无请求体` |
| POST | `/api/admin/task/upload/delete` | `tid 查询参数` |
| POST | `/api/admin/task/upload/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/upload/done` | `无请求体` |
| POST | `/api/admin/task/upload/info` | `tid 查询参数` |
| POST | `/api/admin/task/upload/retry` | `tid 查询参数` |
| POST | `/api/admin/task/upload/retry_failed` | `无请求体` |
| POST | `/api/admin/task/upload/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/admin/task/upload/undone` | `无请求体` |
| POST | `/api/admin/transcode/cancel` | `server/handles.CancelTranscodeJob.req` |
| GET | `/api/admin/transcode/jobs` | `server/handles.ListTranscodeJobs` |
| GET | `/api/admin/transcode/workers` | `server/handles.ListTranscodeWorkers` |
| POST | `/api/admin/user/cancel_2fa` | `server/handles.Cancel2FAById` |
| POST | `/api/admin/user/create` | `internal/model.User` |
| POST | `/api/admin/user/del_cache` | `server/handles.DelUserCache` |
| POST | `/api/admin/user/delete` | `server/handles.DeleteUser` |
| GET | `/api/admin/user/get` | `server/handles.GetUser` |
| GET | `/api/admin/user/list` | `internal/model.PageReq` |
| POST | `/api/admin/user/sshkey/delete` | `server/handles.DeletePublicKey` |
| GET | `/api/admin/user/sshkey/list` | `server/handles.ListPublicKeys` |
| POST | `/api/admin/user/update` | `internal/model.User` |
| POST | `/api/auth/2fa/generate` | `server/handles.Generate2FA` |
| POST | `/api/auth/2fa/verify` | `server/handles.Verify2FAReq` |
| GET | `/api/auth/get_sso_id` | `server/handles.SSOLoginCallback` |
| POST | `/api/auth/login` | `server/handles.LoginReq` |
| POST | `/api/auth/login/hash` | `server/handles.LoginReq` |
| POST | `/api/auth/login/ldap` | `server/handles.LoginReq` |
| GET | `/api/auth/logout` | `server/handles.LogOut` |
| GET | `/api/auth/sso` | `server/handles.SSOLoginRedirect` |
| GET | `/api/auth/sso_callback` | `server/handles.SSOLoginCallback` |
| GET | `/api/auth/sso_get_token` | `server/handles.SSOLoginCallback` |
| POST | `/api/authn/delete_authn` | `server/handles.DeleteAuthnLogin.DeleteAuthnReq` |
| GET | `/api/authn/getcredentials` | `server/handles.GetAuthnCredentials` |
| GET | `/api/authn/webauthn_begin_login` | `server/handles.BeginAuthnLogin` |
| GET | `/api/authn/webauthn_begin_registration` | `server/handles.BeginAuthnRegistration` |
| POST | `/api/authn/webauthn_finish_login` | `server/handles.FinishAuthnLogin` |
| POST | `/api/authn/webauthn_finish_registration` | `server/handles.FinishAuthnRegistration` |
| POST | `/api/fs/add_offline_download` | `server/handles.AddOfflineDownloadReq` |
| POST | `/api/fs/archive/decompress` | `server/handles.ArchiveDecompressReq` |
| Any | `/api/fs/archive/list` | `server/handles.ArchiveListReq` |
| Any | `/api/fs/archive/meta` | `server/handles.ArchiveMetaReq` |
| POST | `/api/fs/batch_rename` | `server/handles.BatchRenameReq` |
| POST | `/api/fs/copy` | `server/handles.MoveCopyReq` |
| Any | `/api/fs/dirs` | `server/handles.DirReq` |
| PUT | `/api/fs/form` | `server/handles.FsForm` |
| Any | `/api/fs/get` | `server/handles.FsGetReq` |
| POST | `/api/fs/get_direct_upload_info` | `server/handles.FsGetDirectUploadInfoReq` |
| POST | `/api/fs/link` | `server/handles.MkdirOrLinkReq` |
| Any | `/api/fs/list` | `server/handles.ListReq` |
| GET | `/api/fs/media/album` | `server/handles.PublicGetAlbum` |
| GET | `/api/fs/media/albums` | `server/handles.PublicListAlbums` |
| GET | `/api/fs/media/annotations` | `server/handles.ListMediaAnnotationsAPI` |
| POST | `/api/fs/media/annotations` | `server/handles.mediaAnnotationSyncReq` |
| GET | `/api/fs/media/artists` | `server/handles.PublicListArtists` |
| GET | `/api/fs/media/exif` | `server/handles.PublicGetMediaExif` |
| POST | `/api/fs/media/favorite` | `server/handles.favoriteReq` |
| GET | `/api/fs/media/favorites` | `server/handles.ListMediaFavoritesAPI` |
| GET | `/api/fs/media/folders` | `server/handles.PublicListFolders` |
| GET | `/api/fs/media/item/:id` | `server/handles.PublicGetMedia` |
| GET | `/api/fs/media/list` | `server/handles.PublicListMedia` |
| GET | `/api/fs/media/progress` | `server/handles.GetMediaProgress` |
| POST | `/api/fs/media/progress` | `server/handles.saveProgressReq` |
| GET | `/api/fs/media/resume` | `server/handles.ListMediaResumeAPI` |
| GET | `/api/fs/media/scan_paths` | `server/handles.PublicListScanPaths` |
| GET | `/api/fs/media/stream_url` | `server/handles.GetMediaStreamURL` |
| GET | `/api/fs/media/strm` | `server/handles.GetMediaStrm` |
| GET | `/api/fs/media/userdata` | `server/handles.GetMediaUserDataBatch` |
| POST | `/api/fs/mkdir` | `server/handles.MkdirOrLinkReq` |
| POST | `/api/fs/move` | `server/handles.MoveCopyReq` |
| POST | `/api/fs/multipart/abort` | `server/handles.MultipartAbort` |
| PUT | `/api/fs/multipart/chunk` | `server/handles.MultipartChunk` |
| POST | `/api/fs/multipart/complete` | `server/handles.MultipartComplete` |
| POST | `/api/fs/multipart/init` | `server/handles.MultipartInit` |
| GET | `/api/fs/multipart/status` | `server/handles.MultipartStatus` |
| Any | `/api/fs/other` | `server/handles.FsOtherReq` |
| PUT | `/api/fs/put` | `server/handles.FsStream` |
| POST | `/api/fs/recursive_move` | `server/handles.RecursiveMoveReq` |
| POST | `/api/fs/regex_rename` | `server/handles.RegexRenameReq` |
| POST | `/api/fs/remove` | `server/handles.RemoveReq` |
| POST | `/api/fs/remove_empty_directory` | `server/handles.RemoveEmptyDirectoryReq` |
| POST | `/api/fs/rename` | `server/handles.RenameReq` |
| Any | `/api/fs/search` | `server/handles.SearchReq` |
| POST | `/api/fs/seed/capabilities` | `server/handles.SeedCapabilityReq` |
| POST | `/api/fs/seed/convert` | `server/handles.SeedConvertReq` |
| POST | `/api/fs/seed/diagnose` | `server/handles.SeedDataReq` |
| POST | `/api/fs/seed/generate` | `server/handles.SeedGenerateReq` |
| POST | `/api/fs/seed/offline_download` | `server/handles.SeedQuickSaveReq` |
| POST | `/api/fs/seed/parse` | `server/handles.SeedDataReq` |
| POST | `/api/fs/seed/quick_save` | `server/handles.SeedQuickSaveReq` |
| POST | `/api/fs/seed/rapid_upload` | `server/handles.SeedQuickSaveReq` |
| POST | `/api/fs/seed/update` | `server/handles.SeedUpdateReq` |
| POST | `/api/fs/seed/update_channels` | `server/handles.UpdateSeedChannels` |
| POST | `/api/fs/seed/upload_parse` | `server/handles.UploadSeedAndParse` |
| POST | `/api/fs/torrent/generate` | `server/handles.GenerateTorrentReq` |
| POST | `/api/fs/torrent/parse` | `server/handles.ParseTorrentReq` |
| POST | `/api/fs/torrent/rapid_upload` | `server/handles.TorrentRapidUploadReq` |
| POST | `/api/fs/torrent/upload_parse` | `server/handles.UploadTorrentAndParse` |
| POST | `/api/fs/transcode/play` | `server/handles.TranscodePlay.req` |
| GET | `/api/me` | `server/handles.CurrentUser` |
| POST | `/api/me/appkey/create` | `server/handles.CreateMediaAppKey.req` |
| POST | `/api/me/appkey/delete` | `server/handles.DeleteMediaAppKey` |
| GET | `/api/me/appkey/list` | `server/handles.ListMediaAppKeys` |
| POST | `/api/me/sshkey/add` | `server/handles.SSHKeyAddReq` |
| POST | `/api/me/sshkey/delete` | `server/handles.DeleteMyPublicKey` |
| GET | `/api/me/sshkey/list` | `server/handles.ListMyPublicKey` |
| POST | `/api/me/update` | `internal/model.User` |
| Any | `/api/public/archive_extensions` | `server/handles.ArchiveExtensions` |
| POST | `/api/public/init/setup` | `server/handles.InitSetupReq` |
| Any | `/api/public/init_status` | `server/handles.InitStatus` |
| GET | `/api/public/media/proxy` | `server/handles.PublicMediaProxy` |
| GET | `/api/public/media/thumb` | `server/handles.PublicMediaThumb` |
| GET | `/api/public/media_status` | `server/handles.PublicMediaStatus` |
| Any | `/api/public/offline_download_tools` | `server/handles.OfflineDownloadTools` |
| Any | `/api/public/settings` | `server/handles.PublicSettings` |
| POST | `/api/share/create` | `server/handles.UpdateSharingReq` |
| POST | `/api/share/delete` | `server/handles.DeleteSharing` |
| POST | `/api/share/disable` | `server/handles.SetEnableSharing` |
| POST | `/api/share/enable` | `server/handles.SetEnableSharing` |
| GET | `/api/share/get` | `server/handles.GetSharing` |
| Any | `/api/share/list` | `internal/model.PageReq` |
| POST | `/api/share/update` | `server/handles.UpdateSharingReq` |
| POST | `/api/task/copy/cancel` | `tid 查询参数` |
| POST | `/api/task/copy/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/copy/clear_done` | `无请求体` |
| POST | `/api/task/copy/clear_succeeded` | `无请求体` |
| POST | `/api/task/copy/delete` | `tid 查询参数` |
| POST | `/api/task/copy/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/copy/done` | `无请求体` |
| POST | `/api/task/copy/info` | `tid 查询参数` |
| POST | `/api/task/copy/retry` | `tid 查询参数` |
| POST | `/api/task/copy/retry_failed` | `无请求体` |
| POST | `/api/task/copy/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/copy/undone` | `无请求体` |
| POST | `/api/task/decompress/cancel` | `tid 查询参数` |
| POST | `/api/task/decompress/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/decompress/clear_done` | `无请求体` |
| POST | `/api/task/decompress/clear_succeeded` | `无请求体` |
| POST | `/api/task/decompress/delete` | `tid 查询参数` |
| POST | `/api/task/decompress/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/decompress/done` | `无请求体` |
| POST | `/api/task/decompress/info` | `tid 查询参数` |
| POST | `/api/task/decompress/retry` | `tid 查询参数` |
| POST | `/api/task/decompress/retry_failed` | `无请求体` |
| POST | `/api/task/decompress/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/decompress/undone` | `无请求体` |
| POST | `/api/task/decompress_upload/cancel` | `tid 查询参数` |
| POST | `/api/task/decompress_upload/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/decompress_upload/clear_done` | `无请求体` |
| POST | `/api/task/decompress_upload/clear_succeeded` | `无请求体` |
| POST | `/api/task/decompress_upload/delete` | `tid 查询参数` |
| POST | `/api/task/decompress_upload/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/decompress_upload/done` | `无请求体` |
| POST | `/api/task/decompress_upload/info` | `tid 查询参数` |
| POST | `/api/task/decompress_upload/retry` | `tid 查询参数` |
| POST | `/api/task/decompress_upload/retry_failed` | `无请求体` |
| POST | `/api/task/decompress_upload/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/decompress_upload/undone` | `无请求体` |
| POST | `/api/task/move/cancel` | `tid 查询参数` |
| POST | `/api/task/move/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/move/clear_done` | `无请求体` |
| POST | `/api/task/move/clear_succeeded` | `无请求体` |
| POST | `/api/task/move/delete` | `tid 查询参数` |
| POST | `/api/task/move/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/move/done` | `无请求体` |
| POST | `/api/task/move/info` | `tid 查询参数` |
| POST | `/api/task/move/retry` | `tid 查询参数` |
| POST | `/api/task/move/retry_failed` | `无请求体` |
| POST | `/api/task/move/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/move/undone` | `无请求体` |
| POST | `/api/task/offline_download/cancel` | `tid 查询参数` |
| POST | `/api/task/offline_download/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/offline_download/clear_done` | `无请求体` |
| POST | `/api/task/offline_download/clear_succeeded` | `无请求体` |
| POST | `/api/task/offline_download/delete` | `tid 查询参数` |
| POST | `/api/task/offline_download/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/offline_download/done` | `无请求体` |
| POST | `/api/task/offline_download/info` | `tid 查询参数` |
| POST | `/api/task/offline_download/retry` | `tid 查询参数` |
| POST | `/api/task/offline_download/retry_failed` | `无请求体` |
| POST | `/api/task/offline_download/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/offline_download/undone` | `无请求体` |
| POST | `/api/task/offline_download_transfer/cancel` | `tid 查询参数` |
| POST | `/api/task/offline_download_transfer/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/offline_download_transfer/clear_done` | `无请求体` |
| POST | `/api/task/offline_download_transfer/clear_succeeded` | `无请求体` |
| POST | `/api/task/offline_download_transfer/delete` | `tid 查询参数` |
| POST | `/api/task/offline_download_transfer/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/offline_download_transfer/done` | `无请求体` |
| POST | `/api/task/offline_download_transfer/info` | `tid 查询参数` |
| POST | `/api/task/offline_download_transfer/retry` | `tid 查询参数` |
| POST | `/api/task/offline_download_transfer/retry_failed` | `无请求体` |
| POST | `/api/task/offline_download_transfer/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/offline_download_transfer/undone` | `无请求体` |
| POST | `/api/task/seed_generate/cancel` | `tid 查询参数` |
| POST | `/api/task/seed_generate/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/seed_generate/clear_done` | `无请求体` |
| POST | `/api/task/seed_generate/clear_succeeded` | `无请求体` |
| POST | `/api/task/seed_generate/delete` | `tid 查询参数` |
| POST | `/api/task/seed_generate/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/seed_generate/done` | `无请求体` |
| POST | `/api/task/seed_generate/info` | `tid 查询参数` |
| POST | `/api/task/seed_generate/retry` | `tid 查询参数` |
| POST | `/api/task/seed_generate/retry_failed` | `无请求体` |
| POST | `/api/task/seed_generate/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/seed_generate/undone` | `无请求体` |
| POST | `/api/task/seed_save/cancel` | `tid 查询参数` |
| POST | `/api/task/seed_save/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/seed_save/clear_done` | `无请求体` |
| POST | `/api/task/seed_save/clear_succeeded` | `无请求体` |
| POST | `/api/task/seed_save/delete` | `tid 查询参数` |
| POST | `/api/task/seed_save/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/seed_save/done` | `无请求体` |
| POST | `/api/task/seed_save/info` | `tid 查询参数` |
| GET | `/api/task/seed_save/undone` | `无请求体` |
| POST | `/api/task/upload/cancel` | `tid 查询参数` |
| POST | `/api/task/upload/cancel_some` | `JSON ID 字符串数组` |
| POST | `/api/task/upload/clear_done` | `无请求体` |
| POST | `/api/task/upload/clear_succeeded` | `无请求体` |
| POST | `/api/task/upload/delete` | `tid 查询参数` |
| POST | `/api/task/upload/delete_some` | `JSON ID 字符串数组` |
| GET | `/api/task/upload/done` | `无请求体` |
| POST | `/api/task/upload/info` | `tid 查询参数` |
| POST | `/api/task/upload/retry` | `tid 查询参数` |
| POST | `/api/task/upload/retry_failed` | `无请求体` |
| POST | `/api/task/upload/retry_some` | `JSON ID 字符串数组` |
| GET | `/api/task/upload/undone` | `无请求体` |
| POST | `/api/transcode/worker/claim` | `internal/transcode.ClaimRequest` |
| POST | `/api/transcode/worker/heartbeat` | `internal/transcode.HeartbeatRequest` |
| POST | `/api/transcode/worker/job/finish` | `internal/transcode.FinishRequest` |
| POST | `/api/transcode/worker/register` | `internal/transcode.RegisterRequest` |
| PUT | `/api/transcode/worker/segment` | `server/handles.WorkerSegmentUpload` |

## 请求结构与任务返回结构

以下为源码中的 Go 结构，JSON 字段名、form 字段名及 binding 标签均保留；`json:"-"` 不对外序列化。没有 `binding:"required"` 仅表示绑定器不作该必填检查，handler 仍可能检查内容和权限。字段类型 `interface{}`／`any` 由运行时及驱动决定。


### `internal/db.MediaItemQuery`

源码：`internal/db/media.go:95`。

```go
// MediaItemQuery 媒体条目查询参数
type MediaItemQuery struct {
	MediaType	model.MediaType
	ScanPathID	uint	// 按扫描路径ID筛选
	FolderPath	string	// 按文件夹路径筛选
	TypeTag		string	// 按类型标签筛选（电影、电视剧等）
	ContentTag	string	// 按内容标签筛选（喜剧、惊悚等）
	Artist		string	// 按专辑艺术家筛选（音乐）
	ArtistSet	bool	// 显式空艺术家筛选
	CanAccess	func(*model.MediaItem) bool
	PathPrefix	string	// 按文件夹路径前缀筛选（用于用户 base_path 隔离）
	Hidden		*bool
	Keyword		string
	OrderBy		string	// "name", "date", "size"
	OrderDir	string	// "asc", "desc"
	Page		int
	PageSize	int
}
```


### `internal/driver.SeedRapidUploadRequest`

源码：`internal/driver/driver.go:228`。

```go
// SeedRapidUploadRequest carries everything a driver needs to attempt a
// hash-driven rapid upload (秒传/CAS) without transferring the full content.
//
// It is populated from a parsed transfer seed, so that any cloud drive
// supporting hash-based rapid upload can be used as a seed save target.
type SeedRapidUploadRequest struct {
	// Name is the target file name.
	Name	string
	// Size is the total file size in bytes.
	Size	int64
	// Whole holds whole-file hashes indexed by algorithm (e.g. utils.MD5,
	// utils.SHA1). It must never be nil; individual entries may be empty.
	Whole	*utils.HashInfo
	// SliceSize is the per-slice/piece size in bytes (0 when unknown).
	SliceSize	int64
	// SliceMD5s is the ordered per-slice MD5 list (used by 189pc-style CAS).
	SliceMD5s	[]string
	// SliceMD5 is the final slice digest (uppercase MD5 of the "\n"-joined
	// slice MD5s) that the provider actually compares during dedup. It is
	// authoritative: .cas payloads normally carry only this value, and drivers
	// should use it as-is instead of recomputing from SliceMD5s.
	SliceMD5	string
	// SliceSHA1s is the ordered per-slice SHA1 list (used by SHA1-piece drives).
	SliceSHA1s	[]string
	// Open lazily yields the file content as a model.FileStreamer. Drivers whose
	// rapid-upload protocol needs partial or full content (e.g. a leading
	// pre-hash or a proof-code) may call it; hash-only drivers may ignore it.
	// Open may be nil when no content source is available, in which case
	// drivers that strictly require content must fail gracefully.
	Open	func() (model.FileStreamer, error)
}
```


### `internal/fs.GetArgs`

源码：`internal/fs/fs.go:38`。

```go
type GetArgs struct {
	NoLog			bool
	WithStorageDetails	bool
}
```


### `internal/fs.GetStoragesArgs`

源码：`internal/fs/fs.go:176`。

```go
type GetStoragesArgs struct {
}
```


### `internal/fs.ListArgs`

源码：`internal/fs/fs.go:21`。

```go
type ListArgs struct {
	Refresh			bool
	NoLog			bool
	WithStorageDetails	bool
}
```


### `internal/fs.SeedGenerateParams`

源码：`internal/fs/seed_generate.go:68`。

```go
// SeedGenerateParams carries a fully-resolved seed generation request, free of
// any HTTP transport concerns so it can run synchronously or as a task.
type SeedGenerateParams struct {
	Paths			[]string
	Formats			[]string
	Name			string
	Comment			string
	FileComments		map[string]string
	HashMatrix		SeedHashMatrix
	PieceSize		int64
	Trackers		[]string
	Channels		[]torrent.SeedChannel
	OutputPath		string
	IncludeShare		bool
	IncludeDirectSource	bool
	ShareFiles		[]string
	DirectFiles		[]string

	// PreserveStructure 目录展开后是否保留目录结构；默认 false（拍平成文件名）。
	PreserveStructure	bool
	// Split 为 true 时每个文件生成一个种子；默认 false（全部合并成一个）。
	Split	bool

	// OnProgress 可选：读取文件计算哈希时回调 (已读字节, 总字节)，用于任务进度展示。
	OnProgress	func(read, total int64)
	// OnStage 可选：进入新阶段时回调，用于展示「正在计算哈希 2/5」这类状态文案。
	OnStage	func(stage string)
}
```


### `internal/fs.SeedHashMatrix`

源码：`internal/fs/seed_generate.go:60`。

```go
// SeedHashMatrix controls the optional hash metadata stored in a seed.
type SeedHashMatrix struct {
	MD5	SeedHashSelection	`json:"md5"`
	SHA1	SeedHashSelection	`json:"sha1"`
	SHA256	SeedHashSelection	`json:"sha256"`
}
```


### `internal/fs.SeedHashSelection`

源码：`internal/fs/seed_generate.go:54`。

```go
// SeedHashSelection controls whole-file and piece hash inclusion.
type SeedHashSelection struct {
	Whole	bool	`json:"whole"`
	Pieces	bool	`json:"pieces"`
}
```


### `internal/media/scraper.TMDBStats`

源码：`internal/media/scraper/tmdb.go:436`。

```go
// TMDBStats TMDB 刮削统计（线程安全：原子计数）
// 仅记录一次进程级别的累积值；如需更精细可按 ScraperID 分桶
type TMDBStats struct {
	TotalScraped		int64	// 总刮削请求次数（每次 ScrapeVideo 调用 +1）
	HitCount		int64	// 成功刮到结果的次数（搜索阶段命中即停）
	NoMatchCount		int64	// 完全 0 命中的次数
	LowConfidenceCount	int64	// 命中但低于阈值，使用了「最佳兜底」结果的次数
	TotalAttempts		int64	// 累计 TMDB 搜索请求次数（不含详情）
	TotalSearchMillis	int64	// 累计搜索耗时（毫秒）
}
```


### `internal/message.Req`

源码：`internal/message/http.go:16`。

```go
type Req struct {
	Message string `json:"message" form:"message"`
}
```


### `internal/model.ArchiveArgs`

源码：`internal/model/args.go:71`。

```go
type ArchiveArgs struct {
	Password	string
	LinkArgs
}
```


### `internal/model.ArchiveDecompressArgs`

源码：`internal/model/args.go:91`。

```go
type ArchiveDecompressArgs struct {
	ArchiveInnerArgs
	CacheFull	bool
	PutIntoNewDir	bool
	Overwrite	bool
}
```


### `internal/model.ArchiveInnerArgs`

源码：`internal/model/args.go:76`。

```go
type ArchiveInnerArgs struct {
	ArchiveArgs
	InnerPath	string
}
```


### `internal/model.ArchiveListArgs`

源码：`internal/model/args.go:86`。

```go
type ArchiveListArgs struct {
	ArchiveInnerArgs
	Refresh	bool
}
```


### `internal/model.ArchiveMetaArgs`

源码：`internal/model/args.go:81`。

```go
type ArchiveMetaArgs struct {
	ArchiveArgs
	Refresh	bool
}
```


### `internal/model.EpisodeInfo`

源码：`internal/model/media.go:121`。

```go
// EpisodeInfo 选集信息（文件夹型媒体的单集描述）
type EpisodeInfo struct {
	FileName	string	`json:"file_name"`	// 原始文件名（含扩展名）
	Index		int	`json:"index"`		// 序号，默认0，文件名开头有数字则取该数字
	Title		string	`json:"title"`		// 选集标题（去掉序号后的文件名，不含扩展名）
	// Season 季号，0 表示未识别（按第 1 季展示）。
	// 扫描时会从「第一季 / Season 1 / S01」这类子目录名解析出来，
	// 一部剧的多个季合并成一条记录后，靠它才能在播放器 / Emby 里按季分开。
	Season	int	`json:"season,omitempty"`
}
```


### `internal/model.IndexProgress`

源码：`internal/model/search.go:8`。

```go
type IndexProgress struct {
	ObjCount	uint64		`json:"obj_count"`
	IsDone		bool		`json:"is_done"`
	LastDoneTime	*time.Time	`json:"last_done_time"`
	Error		string		`json:"error"`
}
```


### `internal/model.Link`

源码：`internal/model/args.go:28`。

```go
type Link struct {
	URL		string		`json:"url"`	// most common way
	Header		http.Header	`json:"header"`	// needed header (for url)
	RangeReader	RangeReaderIF	`json:"-"`	// recommended way if can't use URL

	Expiration	*time.Duration	// local cache expiration; not transferred by Clone

	//for accelerating request, use multi-thread downloading
	Concurrency	int	`json:"concurrency"`
	PartSize	int	`json:"part_size"`
	ContentLength	int64	`json:"content_length"`	// 转码视频、缩略图

	utils.SyncClosers	`json:"-"`
	// 如果SyncClosers中的资源被关闭后Link将不可用，则此值应为 true
	RequireReference	bool	`json:"-"`
}
```


### `internal/model.LinkArgs`

源码：`internal/model/args.go:21`。

```go
type LinkArgs struct {
	IP		string
	Header		http.Header
	Type		string
	Redirect	bool
}
```


### `internal/model.MediaAnnotation`

源码：`internal/model/media_extra.go:68`。

```go
// MediaAnnotation 用户在 PDF 阅读器里添加的批注。
//
// 只存查看器原生的批注对象（Anchor，JSON）：页码、rect/quadPoints、颜色、
// 文字、绘制点集都在里面，拆成独立列会出现两份可能不一致的数据。
// 重新打开时原样回灌给 PDF 引擎，因此这里不重复建模。
type MediaAnnotation struct {
	ID		uint		`gorm:"primarykey"                    json:"id"`
	CreatedAt	time.Time	`json:"created_at"`
	UpdatedAt	time.Time	`json:"updated_at"`

	UserID	uint	`gorm:"uniqueIndex:idx_media_annotation_key;not null"       json:"-"`
	ItemID	uint	`gorm:"uniqueIndex:idx_media_annotation_key;not null;index" json:"item_id"`
	// RemoteID 是查看器分配的批注 id：多设备打开同一份 PDF 时靠它做去重与更新
	RemoteID	string	`gorm:"uniqueIndex:idx_media_annotation_key;size:128;not null" json:"remote_id"`
	Anchor		string	`gorm:"type:text;not null"                                json:"anchor"`
}
```


### `internal/model.MediaAppKey`

源码：`internal/model/media_extra.go:8`。

```go
// MediaAppKey 媒体应用密钥。
// 用于无法走交互式登录的客户端（Subsonic 的 token 认证、Emby 的 api_key、
// MoviePilot 对接等）。密钥本身以 AES-GCM 加密存储，数据库中不保留明文。
type MediaAppKey struct {
	ID		uint		`gorm:"primarykey"                  json:"id"`
	CreatedAt	time.Time	`json:"created_at"`
	UpdatedAt	time.Time	`json:"updated_at"`

	UserID	uint	`gorm:"index;not null"       json:"user_id"`
	Name	string	`json:"name"`	// 备注名，例如 "Symfonium"、"MoviePilot"
	// 前缀由 AppKeyPrefix 固定为最多 8 个 ASCII 字符；显式长度使 MySQL
	// 将索引列建为 varchar，而不是无法建立索引的 longtext。
	Prefix	string	`gorm:"size:32;index"           json:"prefix"`	// 明文前缀（便于识别/排查）
	Secret	string	`gorm:"not null"             json:"-"`		// AES-GCM 密文（base64）

	LastUsedAt	*time.Time	`json:"last_used_at"`
}
```


### `internal/model.MediaItem`

源码：`internal/model/media.go:47`。

```go
// MediaItem 媒体条目（统一表，通过 media_type 区分类型，便于后期扩展新类型）
type MediaItem struct {
	// 显式声明基础字段，避免同时嵌入 gorm.Model 后重复生成 deleted_at 索引列；
	// JSON 标签继续保持媒体 API 的字段命名约定。
	ID		uint		`gorm:"primarykey"           json:"id"`
	CreatedAt	time.Time	`json:"created_at"`
	UpdatedAt	time.Time	`json:"updated_at"`
	DeletedAt	gorm.DeletedAt	`gorm:"index"                json:"-"`
	// 基础信息
	MediaType	MediaType	`gorm:"size:32;index;not null"  json:"media_type"`
	ScanPathID	uint		`gorm:"index"                   json:"scan_path_id"`	// 关联的扫描路径ID
	// 三个字段共同组成复合唯一索引。MySQL 对「有索引且未指定长度」的 string 会生成 longtext，
	// 而 TEXT 列不能直接建索引（AutoMigrate 会失败）；因此必须显式限定 size。
	// utf8mb4 下 InnoDB 索引总长上限 3072 字节：191*3*4 = 2292，留足余量。
	FileName	string	`gorm:"size:191;uniqueIndex:idx_media_folder_file_album" json:"file_name"`
	FileSize	int64	`json:"file_size"`
	MimeType	string	`json:"mime_type"`
	Hidden		bool	`gorm:"default:false"           json:"hidden"`

	// 刮削/编辑信息
	ScrapedName	string	`json:"scraped_name"`
	Description	string	`gorm:"type:text"               json:"description"`
	Cover		string	`json:"cover"`					// 封面URL或本地路径
	ReleaseDate	string	`json:"release_date"`				// 发布时间，格式 YYYY-MM-DD
	Rating		float32	`json:"rating"`					// 评分 0-10
	Genre		string	`json:"genre"`					// 类型，逗号分隔，如 "动作,科幻"
	Authors		string	`gorm:"type:text"               json:"authors"`	// 作者/演员，JSON数组字符串
	Plot		string	`gorm:"type:text"               json:"plot"`	// 剧情/内容介绍
	Reviews		string	`gorm:"type:text"               json:"reviews"`	// 用户评价，JSON数组字符串

	// 外部ID（用于刮削关联）
	ExternalID	string	`json:"external_id"`	// TMDB ID / Discogs ID / 豆瓣ID

	// 音乐专属字段
	AlbumName	string	`gorm:"size:191;uniqueIndex:idx_media_folder_file_album" json:"album_name"`	// 所属专辑名
	AlbumArtist	string	`json:"album_artist"`								// 专辑艺术家
	TrackNumber	int	`json:"track_number"`								// 曲目编号
	Duration	int	`json:"duration"`								// 时长（秒）
	Lyrics		string	`gorm:"type:text"    json:"lyrics"`						// LRC格式歌词

	// 视频专属字段
	VideoType	string	`json:"video_type"`	// "movie" 或 "tv"
	Season		int	`json:"season"`		// 季（电视剧）
	Episode		int	`json:"episode"`	// 集（电视剧）

	// 书籍专属字段
	Publisher	string	`json:"publisher"`	// 出版社
	ISBN		string	`json:"isbn"`		// ISBN

	// 目录合并模式
	IsFolder	bool	`gorm:"default:false"                                    json:"is_folder"`		// 是否为文件夹模式条目
	FolderPath	string	`gorm:"size:191;index;uniqueIndex:idx_media_folder_file_album" json:"folder_path"`	// 扫描根路径（恒定为扫描路径，与file_name+album_name组合唯一）
	Episodes	string	`gorm:"type:text"                                         json:"episodes"`		// 选集信息，JSON数组，格式：[{file_name,index,title},...]

	ScrapedAt	*time.Time	`json:"scraped_at"`
}
```


### `internal/model.MediaScanPath`

源码：`internal/model/media.go:20`。

```go
// MediaScanPath 媒体库扫描路径（每个媒体库可配置多个扫描路径）
type MediaScanPath struct {
	ID		uint		`gorm:"primarykey"           json:"id"`
	CreatedAt	time.Time	`json:"created_at"`
	UpdatedAt	time.Time	`json:"updated_at"`
	DeletedAt	gorm.DeletedAt	`gorm:"index"                json:"-"`

	MediaType	MediaType	`gorm:"size:32;index;not null" json:"media_type"`	// 所属媒体类型
	Name		string		`json:"name"`						// 路径名称（用于前端筛选显示）
	Path		string		`gorm:"not null"             json:"path"`		// VFS 扫描路径
	PathMerge	bool		`gorm:"default:false"        json:"path_merge"`		// 路径合并模式（子文件夹作为一个条目）
	// MergeDepth 合并模式的固定层级深度（相对扫描根，0 表示自动）。
	//   0 = 旧行为：逐层向下，第一个「直接包含媒体文件」的文件夹作为一个条目
	//   N = 第 N 层文件夹整体作为一个条目，内部的媒体文件（含更深层子目录）都算选集
	// 扫描根的直接子文件夹是第 1 层，所以 /电视剧/<剧名>/第N季/... 应填 1，
	// 整部剧聚合为一条记录，只刮削一次。名字形如「第一季 / Season 1 / S01」的
	// 目录即使被层级规则选中，也会被自动上移到剧名目录，不会单独成条。
	MergeDepth	int		`gorm:"default:0"            json:"merge_depth"`
	TypeTag		string		`json:"type_tag"`					// 类型标签：电影、电视剧等
	ContentTags	string		`json:"content_tags"`					// 内容标签：喜剧、惊悚等（逗号分隔）
	EnableScrape	bool		`gorm:"default:true"         json:"enable_scrape"`	// 是否启用刮削
	LastScanAt	*time.Time	`json:"last_scan_at"`					// 最后扫描时间
}
```


### `internal/model.MediaType`

源码：`internal/model/media.go:10`。

```go
// MediaType 媒体类型，使用字符串便于后期扩展
type MediaType string
```


### `internal/model.MediaUserData`

源码：`internal/model/media_extra.go:24`。

```go
// MediaUserData 用户对媒体条目的播放状态，Emby 与 Subsonic 共用。
type MediaUserData struct {
	ID		uint		`gorm:"primarykey"                                  json:"id"`
	CreatedAt	time.Time	`json:"created_at"`
	UpdatedAt	time.Time	`json:"updated_at"`

	UserID		uint	`gorm:"uniqueIndex:idx_media_user_item_ep;not null"   json:"user_id"`
	ItemID		uint	`gorm:"uniqueIndex:idx_media_user_item_ep;not null"   json:"item_id"`
	EpisodeIndex	int	`gorm:"uniqueIndex:idx_media_user_item_ep;not null"   json:"episode_index"`	// 0 表示条目本身，>0 表示选集序号

	Played		bool		`json:"played"`
	PlayCount	int		`json:"play_count"`
	IsFavorite	bool		`gorm:"index"                                  json:"is_favorite"`
	Rating		int		`json:"rating"`		// 0-5，Subsonic 语义
	PositionTicks	int64		`json:"position_ticks"`	// 100ns tick，与 Emby 一致
	LastPlayedAt	*time.Time	`json:"last_played_at"`
}
```


### `internal/model.Meta`

源码：`internal/model/meta.go:3`。

```go
type Meta struct {
	ID		uint	`json:"id" gorm:"primaryKey"`
	Path		string	`json:"path" gorm:"unique" binding:"required"`
	ReadUsers	[]uint	`json:"read_users" gorm:"serializer:json"`
	ReadUsersSub	bool	`json:"read_users_sub"`
	WriteUsers	[]uint	`json:"write_users" gorm:"serializer:json"`
	WriteUsersSub	bool	`json:"write_users_sub"`
	Password	string	`json:"password"`
	PSub		bool	`json:"p_sub"`
	Write		bool	`json:"write"`
	WSub		bool	`json:"w_sub"`
	Hide		string	`json:"hide"`
	HSub		bool	`json:"h_sub"`
	Readme		string	`json:"readme"`
	RSub		bool	`json:"r_sub"`
	Header		string	`json:"header"`
	HeaderSub	bool	`json:"header_sub"`
}
```


### `internal/model.ObjMask`

源码：`internal/model/obj.go:247`。

```go
type ObjMask uint8
```


### `internal/model.Object`

源码：`internal/model/object.go:22`。

```go
type Object struct {
	ID		string
	Path		string
	Name		string
	Size		int64
	Modified	time.Time
	Ctime		time.Time	// file create time
	IsFolder	bool
	HashInfo	utils.HashInfo
	Mask		ObjMask
}
```


### `internal/model.PageReq`

源码：`internal/model/req.go:3`。

```go
type PageReq struct {
	Page	int	`json:"page" form:"page"`
	PerPage	int	`json:"per_page" form:"per_page"`
}
```


### `internal/model.SSHPublicKey`

源码：`internal/model/sshkey.go:8`。

```go
type SSHPublicKey struct {
	ID		uint		`json:"id" gorm:"primaryKey"`
	UserId		uint		`json:"-"`
	Title		string		`json:"title"`
	Fingerprint	string		`json:"fingerprint"`
	KeyStr		string		`gorm:"type:text" json:"-"`
	AddedTime	time.Time	`json:"added_time"`
	LastUsedTime	time.Time	`json:"last_used_time"`
}
```


### `internal/model.SettingItem`

源码：`internal/model/setting.go:27`。

```go
type SettingItem struct {
	Key		string	`json:"key" gorm:"primaryKey" binding:"required"`	// unique key
	Value		string	`json:"value"`						// value
	MigrationValue	string	`json:"-" gorm:"-:all"`					// deprecated value
	Help		string	`json:"help"`						// help message
	Type		string	`json:"type"`						// string, number, bool, select
	Options		string	`json:"options"`					// values for select
	Group		int	`json:"group"`						// use to group setting in frontend
	Flag		int	`json:"flag"`						// 0 = public, 1 = private, 2 = readonly, 3 = deprecated, etc.
	Index		uint	`json:"index"`
}
```


### `internal/model.Sharing`

源码：`internal/model/sharing.go:20`。

```go
type Sharing struct {
	*SharingDB
	Files	[]string	`json:"files"`
	Creator	*User		`json:"-"`
}
```


### `internal/model.SharingArchiveListArgs`

源码：`internal/model/args.go:110`。

```go
type SharingArchiveListArgs struct {
	ArchiveListArgs
	Pwd		string
	ClientIP	string
}
```


### `internal/model.SharingArchiveMetaArgs`

源码：`internal/model/args.go:104`。

```go
type SharingArchiveMetaArgs struct {
	ArchiveMetaArgs
	Pwd		string
	ClientIP	string
}
```


### `internal/model.SharingDB`

源码：`internal/model/sharing.go:5`。

```go
type SharingDB struct {
	ID		string		`json:"id" gorm:"type:varchar(64);primaryKey"`
	FilesRaw	string		`json:"-" gorm:"type:text"`
	Expires		*time.Time	`json:"expires"`
	Pwd		string		`json:"pwd"`
	Accessed	int		`json:"accessed"`
	MaxAccessed	int		`json:"max_accessed"`
	CreatorId	uint		`json:"-"`
	Disabled	bool		`json:"disabled"`
	Remark		string		`json:"remark"`
	Readme		string		`json:"readme" gorm:"type:text"`
	Header		string		`json:"header" gorm:"type:text"`
	Sort
}
```


### `internal/model.SharingListArgs`

源码：`internal/model/args.go:98`。

```go
type SharingListArgs struct {
	Refresh		bool
	Pwd		string
	ClientIP	string
}
```


### `internal/model.Sort`

源码：`internal/model/storage.go:27`。

```go
type Sort struct {
	OrderBy		string	`json:"order_by"`
	OrderDirection	string	`json:"order_direction"`
	ExtractFolder	string	`json:"extract_folder"`
}
```


### `internal/model.Storage`

源码：`internal/model/storage.go:8`。

```go
type Storage struct {
	ID			uint		`json:"id" gorm:"primaryKey"`				// unique key
	MountPath		string		`json:"mount_path" gorm:"unique" binding:"required"`	// must be standardized
	Order			int		`json:"order"`						// use to sort
	Driver			string		`json:"driver"`						// driver used
	CacheExpiration		int		`json:"cache_expiration"`				// cache expire time
	CustomCachePolicies	string		`json:"custom_cache_policies" gorm:"type:text"`
	Status			string		`json:"status"`
	Addition		string		`json:"addition" gorm:"type:text"`	// Additional information, defined in the corresponding driver
	Remark			string		`json:"remark"`
	Modified		time.Time	`json:"modified"`
	Disabled		bool		`json:"disabled"`	// if disabled
	DisableIndex		bool		`json:"disable_index"`
	EnableSign		bool		`json:"enable_sign"`
	SeedPolicy		string		`json:"seed_policy" gorm:"default:inherit"`
	Sort
	Proxy
}
```


### `internal/model.StorageDetails`

源码：`internal/model/storage.go:79`。

```go
type StorageDetails struct {
	DiskUsage
}
```


### `internal/model.User`

源码：`internal/model/user.go:40`。

```go
type User struct {
	ID		uint	`json:"id" gorm:"primaryKey"`				// unique key
	Username	string	`json:"username" gorm:"unique" binding:"required"`	// username
	PwdHash		string	`json:"-"`						// password hash
	PwdTS		int64	`json:"-"`						// password timestamp
	Salt		string	`json:"-"`						// unique salt
	Password	string	`json:"password"`					// password
	BasePath	string	`json:"base_path"`					// base path
	Role		int	`json:"role"`						// user's role
	Disabled	bool	`json:"disabled"`
	// Determine permissions by bit
	//   0:  can see hidden files
	//   1:  can access without password
	//   2:  can add offline download tasks
	//   3:  can mkdir and upload
	//   4:  can rename
	//   5:  can move
	//   6:  can copy
	//   7:  can remove
	//   8:  webdav read
	//   9:  webdav write
	//   10: ftp/sftp login and read
	//   11: ftp/sftp write
	//   12: can read archives
	//   13: can decompress archives
	//   14: can share
	//   15: can customize share id
	Permission	int32	`json:"permission"`
	OtpSecret	string	`json:"-"`
	SsoID		string	`json:"sso_id"`	// unique by sso platform
	Authn		string	`gorm:"type:text" json:"-"`
	AllowLdap	bool	`json:"allow_ldap" gorm:"default:true"`
}
```


### `internal/multipart.InitReq`

源码：`internal/multipart/session.go:155`。

```go
// InitReq carries everything the handler parsed from the init request.
type InitReq struct {
	User		*model.User
	Path		string	// full destination path, already user-joined
	Size		int64
	ChunkSize	int64	// final chunk size in bytes, already clamped by the handler
	Mimetype	string
	Modified	time.Time
	Hashes		map[*utils.HashType]string
}
```


### `internal/multipart.State`

源码：`internal/multipart/session.go:26`。

```go
type State string
```


### `internal/offline_download/tool.AddURLArgs`

源码：`internal/offline_download/tool/add.go:42`。

```go
type AddURLArgs struct {
	URL		string
	DstDirPath	string
	Tool		string
	DeletePolicy	DeletePolicy
}
```


### `internal/offline_download/tool.CloudDeleteError`

源码：`internal/offline_download/tool/download_115_delete.go:30`。

```go
// CloudDeleteError carries a stable reason for API clients while preserving
// the underlying conflict, cancellation or persistence error.
type CloudDeleteError struct {
	Reason	string
	Err	error
}
```


### `internal/offline_download/tool.DeletePolicy`

源码：`internal/offline_download/tool/add.go:32`。

```go
type DeletePolicy string
```


### `internal/offline_download/tool.Open115TaskInfo`

源码：`internal/offline_download/tool/download_115_info.go:5`。

```go
// Open115TaskInfo is the public receipt of an asynchronous 115 Open download.
// Account IDs and storage bindings are deliberately kept out of the response.
type Open115TaskInfo struct {
	Tool				string	`json:"tool"`
	InfoHash			string	`json:"info_hash"`
	SubmissionState			string	`json:"submission_state"`
	CloudDeleteState		string	`json:"cloud_delete_state"`
	CanDeleteCloudRecord		bool	`json:"can_delete_cloud_record"`
	CloudDeleteUnavailableReason	string	`json:"cloud_delete_unavailable_reason"`
}
```


### `internal/stream.FileStream`

源码：`internal/stream/stream.go:23`。

```go
type FileStream struct {
	Ctx	context.Context
	model.Obj
	io.Reader
	Mimetype		string
	WebPutAsTask		bool
	ForceStreamUpload	bool
	Exist			model.Obj	//the file existed in the destination, we can reuse some info since we wil overwrite it
	utils.Closers
	size		int64
	oriReader	io.Reader	// the original reader, used for caching
	hc		*hcache.HybridCache
	peek		buffer.SizedReadAtSeeker
}
```


### `internal/transcode.ClaimRequest`

源码：`internal/transcode/protocol.go:44`。

```go
type ClaimRequest struct {
	WorkerID	string	`json:"worker_id"`
	Slots		int	`json:"slots"`
	Wait		int	`json:"wait"`	// 长轮询超时秒，0=不等
}
```


### `internal/transcode.ClaimResponse`

源码：`internal/transcode/protocol.go:50`。

```go
type ClaimResponse struct {
	Jobs []*Job `json:"jobs"`
}
```


### `internal/transcode.DecideRequest`

源码：`internal/transcode/decide.go:11`。

```go
// DecideRequest 决策器输入
type DecideRequest struct {
	FileName	string
	FileSize	int64		// 字节
	Probe		SourceProbe	// 可选；为空则只用大小+后缀判断
}
```


### `internal/transcode.FinishRequest`

源码：`internal/transcode/protocol.go:56`。

```go
type FinishRequest struct {
	JobID	string		`json:"job_id"`
	Status	JobStatus	`json:"status"`
	Error	string		`json:"error,omitempty"`
	Stats	JobStats	`json:"stats"`
}
```


### `internal/transcode.HeartbeatRequest`

源码：`internal/transcode/protocol.go:29`。

```go
type HeartbeatRequest struct {
	WorkerID	string		`json:"worker_id"`
	Load		float64		`json:"load"`
	Running		[]string	`json:"running"`
	FreeSlots	int		`json:"free_slots"`
}
```


### `internal/transcode.HeartbeatResponse`

源码：`internal/transcode/protocol.go:36`。

```go
type HeartbeatResponse struct {
	OK	bool		`json:"ok"`
	Kick	bool		`json:"kick"`			// 服务端要求 worker 退出
	Cancel	[]string	`json:"cancel,omitempty"`	// 需要取消的 job ids
}
```


### `internal/transcode.Job`

源码：`internal/transcode/types.go:77`。

```go
// Job 一次转码任务
type Job struct {
	ID		string		`json:"id"`
	Path		string		`json:"path"`		// OpenList 文件路径
	SourceURL	string		`json:"source_url"`	// 提供给 Worker 的签名 URL
	Probe		SourceProbe	`json:"probe"`
	Profiles	[]Profile	`json:"profiles"`
	Output		OutputSpec	`json:"output"`
	CallbackToken	string		`json:"callback_token"`	// Worker 回吐切片用（同时具备写权限，绝不外泄给播放端）
	// PlayToken 播放凭据：只用于 master/variant playlist 与切片下发，
	// 与 Worker 回写凭据（CallbackToken）分离，避免播放器拿到可写权限。
	// json:"-" 保证下发给 Worker 的任务描述里不会包含它。
	PlayToken	string	`json:"-"`
	Deadline	int64	`json:"deadline"`	// unix 秒

	// OwnerID 发起转码者的配额键，仅服务端用于去重与配额，不下发给 Worker。
	// 用字符串而不是用户 ID：访客共用一个账号，需要按 "guest:<clientIP>" 拆分配额，
	// 否则单个匿名访客就能占满所有匿名转码配额。
	OwnerID	string	`json:"-"`

	// 运行时
	Status		JobStatus	`json:"status"`
	WorkerID	string		`json:"worker_id"`
	CreatedAt	time.Time	`json:"created_at"`
	StartedAt	time.Time	`json:"started_at"`
	FinishedAt	time.Time	`json:"finished_at"`
	LastAccessAt	time.Time	`json:"last_access_at"`	// 播放端最后一次请求 segment/playlist 的时间
	Error		string		`json:"error,omitempty"`

	// 内部：用于"边转边播"——首切片就绪后关闭该 channel
	readyOnce	sync.Once
	readyCh		chan struct{}
	mu		sync.RWMutex
}
```


### `internal/transcode.JobStats`

源码：`internal/transcode/protocol.go:63`。

```go
type JobStats struct {
	Elapsed		float64	`json:"elapsed"`
	FPS		float64	`json:"fps"`
	Speed		float64	`json:"speed"`
	BytesOut	int64	`json:"bytes_out"`
}
```


### `internal/transcode.JobStatus`

源码：`internal/transcode/types.go:14`。

```go
// JobStatus 转码任务状态
type JobStatus string
```


### `internal/transcode.OutputFormat`

源码：`internal/transcode/types.go:26`。

```go
// OutputFormat 输出封装格式
type OutputFormat string
```


### `internal/transcode.OutputSpec`

源码：`internal/transcode/types.go:71`。

```go
// OutputSpec 输出参数
type OutputSpec struct {
	Format		OutputFormat	`json:"format"`
	SegmentDuration	int		`json:"segment_duration"`	// 秒
}
```


### `internal/transcode.Profile`

源码：`internal/transcode/types.go:60`。

```go
// Profile 输出转码档位
type Profile struct {
	Name		string	`json:"name"`		// 例如 1080p
	VideoCodec	string	`json:"video_codec"`	// h264 / hevc / av1
	VideoBitrate	string	`json:"video_bitrate"`	// 例如 4000k
	Scale		string	`json:"scale"`		// 例如 1920:-2
	AudioCodec	string	`json:"audio_codec"`	// aac / mp3 / opus / copy
	AudioBitrate	string	`json:"audio_bitrate"`	// 例如 160k
	HWAccel		string	`json:"hwaccel"`	// worker 端如何选择硬件加速
}
```


### `internal/transcode.RegisterRequest`

源码：`internal/transcode/protocol.go:8`。

```go
type RegisterRequest struct {
	Name		string		`json:"name"`
	Version		string		`json:"version"`
	Capacity	int		`json:"capacity"`
	HWAccel		[]string	`json:"hwaccel"`
	CodecsDecode	[]string	`json:"codecs_decode"`
	CodecsEncode	[]string	`json:"codecs_encode"`
	MaxResolution	string		`json:"max_resolution"`
	Tags		[]string	`json:"tags"`
	Endpoint	string		`json:"endpoint,omitempty"`
}
```


### `internal/transcode.RegisterResponse`

源码：`internal/transcode/protocol.go:20`。

```go
type RegisterResponse struct {
	WorkerID		string	`json:"worker_id"`
	HeartbeatInterval	int	`json:"heartbeat_interval"`
	ClaimStrategy		string	`json:"claim_strategy"`	// pull
	ProtocolVersion		string	`json:"protocol_version"`
}
```


### `internal/transcode.SourceProbe`

源码：`internal/transcode/types.go:48`。

```go
// SourceProbe 源文件探测信息（由 ffprobe 得出）
type SourceProbe struct {
	Duration	float64	`json:"duration"`	// 秒
	Size		int64	`json:"size"`		// 字节
	VideoCodec	string	`json:"video_codec"`	// h264 / hevc / av1 / vp9 ...
	VideoBitrate	int64	`json:"video_bitrate"`	// bps
	Width		int	`json:"width"`
	Height		int	`json:"height"`
	AudioCodec	string	`json:"audio_codec"`
	AudioBitrate	int64	`json:"audio_bitrate"`
}
```


### `pkg/http_range.Range`

源码：`pkg/http_range/range.go:14`。

```go
// Range specifies the byte range to be sent to the client.
type Range struct {
	Start	int64
	Length	int64	// limit of bytes to read, -1 for unlimited
}
```


### `pkg/torrent.HashWriter`

源码：`pkg/torrent/hash_writer.go:18`。

```go
// HashWriter 同时计算文件的 MD5、分片 MD5 和 SHA-1 piece hash
// 用于在上传过程中一次性计算所有需要的哈希值
type HashWriter struct {
	// 整文件 MD5
	fileMD5	hash.Hash
	// fileSHA1 and fileSHA256 complete the portable full-file matrix.
	fileSHA1	hash.Hash
	fileSHA256	hash.Hash
	// fileGCID 用于迅雷、PikPak 等
	fileGCID	hash.Hash
	// 当前分片 MD5
	sliceMD5	hash.Hash
	// Per-piece hashers are updated in the same pass as whole-file hashes.
	pieceMD5	hash.Hash
	pieceSHA1	hash.Hash
	pieceSHA256	hash.Hash

	// 分片大小（默认 10MB）
	sliceSize	int64
	// piece 大小（与 sliceSize 相同，保持对齐）
	pieceSize	int64
	// 文件总大小（用于 GCID 初始化）
	fileSize	int64

	// 当前分片已写入字节数
	sliceWritten	int64
	// 当前 piece 已写入字节数
	pieceWritten	int64
	// 总写入字节数
	totalWritten	int64

	// 每个分片的 MD5（大写十六进制）
	sliceMD5Hexs	[]string
	// all standard BitTorrent SHA-1 piece hashes concatenated
	pieceHashes	[]byte
	// portable per-file piece matrix
	pieceMD5Hexs	[]string
	pieceSHA1Hexs	[]string
	pieceSHA256Hexs	[]string
}
```


### `pkg/torrent.SeedChannel`

源码：`pkg/torrent/torrent.go:164`。

```go
// SeedChannel contains only public storage discovery metadata.
type SeedChannel struct {
	Driver		string	`json:"driver"`
	MountPath	string	`json:"mount_path,omitempty"`
}
```


### `pkg/torrent.SeedFile`

源码：`pkg/torrent/torrent.go:170`。

```go
// SeedFile describes one relative file in a sharing seed.
type SeedFile struct {
	Path		string		`json:"path"`
	Size		int64		`json:"size"`
	Modified	string		`json:"modified,omitempty"`
	Comment		string		`json:"comment,omitempty"`
	Hashes		SeedHashes	`json:"hashes"`
	Sources		[]SeedSource	`json:"sources,omitempty"`
	CASSliceMD5	string		`json:"cas_slice_md5,omitempty"`
	CASCreateTime	string		`json:"cas_create_time,omitempty"`
	// CASCloud 标识该文件 CAS 元数据所属的云盘类型（如 "189"）。留空视为 189。
	CASCloud	string		`json:"cas_cloud,omitempty"`
	MissingChannels	[]string	`json:"missing_channels,omitempty"`
}
```


### `pkg/torrent.SeedHashes`

源码：`pkg/torrent/torrent.go:185`。

```go
// SeedHashes contains whole-file and optional per-piece hashes.
type SeedHashes struct {
	MD5	string			`json:"md5,omitempty"`
	SHA1	string			`json:"sha1,omitempty"`
	SHA256	string			`json:"sha256,omitempty"`
	GCID	string			`json:"gcid,omitempty"`
	Pieces	*SeedPieceHashes	`json:"pieces,omitempty"`
}
```


### `pkg/torrent.SeedPieceHashes`

源码：`pkg/torrent/torrent.go:194`。

```go
// SeedPieceHashes contains independent per-file piece hashes.
type SeedPieceHashes struct {
	MD5	[]string	`json:"md5,omitempty"`
	SHA1	[]string	`json:"sha1,omitempty"`
	SHA256	[]string	`json:"sha256,omitempty"`
}
```


### `pkg/torrent.SeedSource`

源码：`pkg/torrent/torrent.go:201`。

```go
// SeedSource is an optional retrievable public or signed source URL.
type SeedSource struct {
	Type		string	`json:"type"`
	URL		string	`json:"url"`
	ExpiresAt	string	`json:"expires_at,omitempty"`
	ShareID		string	`json:"share_id,omitempty"`
}
```


### `pkg/utils.HashInfo`

源码：`pkg/utils/hash.go:189`。

```go
// A HashInfo contains hash string for one or more hashType
type HashInfo struct {
	h map[*HashType]string
}
```


### `pkg/utils.HashType`

源码：`pkg/utils/hash.go:30`。

```go
// HashType indicates a standard hashing algorithm
type HashType struct {
	Width	int
	Name	string
	Alias	string
	NewFunc	func(...any) hash.Hash
}
```


### `server/common.PageResp`

源码：`server/common/resp.go:9`。

```go
type PageResp struct {
	Content	interface{}	`json:"content"`
	Total	int64		`json:"total"`
}
```


### `server/handles.AddOfflineDownloadReq`

源码：`server/handles/offline_download.go:324`。

```go
type AddOfflineDownloadReq struct {
	Urls		[]string	`json:"urls"`
	Path		string		`json:"path"`
	Tool		string		`json:"tool"`
	DeletePolicy	string		`json:"delete_policy"`
}
```


### `server/handles.ArchiveContentResp`

源码：`server/handles/archive.go:41`。

```go
type ArchiveContentResp struct {
	ObjResp
	Children	[]ArchiveContentResp	`json:"children"`
}
```


### `server/handles.ArchiveDecompressReq`

源码：`server/handles/archive.go:227`。

```go
type ArchiveDecompressReq struct {
	SrcDir		string		`json:"src_dir" form:"src_dir"`
	DstDir		string		`json:"dst_dir" form:"dst_dir"`
	Names		[]string	`json:"name" form:"name"`
	ArchivePass	string		`json:"archive_pass" form:"archive_pass"`
	InnerPath	string		`json:"inner_path" form:"inner_path"`
	CacheFull	bool		`json:"cache_full" form:"cache_full"`
	PutIntoNewDir	bool		`json:"put_into_new_dir" form:"put_into_new_dir"`
	Overwrite	bool		`json:"overwrite" form:"overwrite"`
}
```


### `server/handles.ArchiveListReq`

源码：`server/handles/archive.go:150`。

```go
type ArchiveListReq struct {
	ArchiveMetaReq
	model.PageReq
	InnerPath	string	`json:"inner_path" form:"inner_path"`
}
```


### `server/handles.ArchiveMetaReq`

源码：`server/handles/archive.go:25`。

```go
type ArchiveMetaReq struct {
	Path		string	`json:"path" form:"path"`
	Password	string	`json:"password" form:"password"`
	Refresh		bool	`json:"refresh" form:"refresh"`
	ArchivePass	string	`json:"archive_pass" form:"archive_pass"`
}
```


### `server/handles.ArchiveMetaResp`

源码：`server/handles/archive.go:32`。

```go
type ArchiveMetaResp struct {
	Comment		string			`json:"comment"`
	IsEncrypted	bool			`json:"encrypted"`
	Content		[]ArchiveContentResp	`json:"content"`
	Sort		*model.Sort		`json:"sort,omitempty"`
	RawURL		string			`json:"raw_url"`
	Sign		string			`json:"sign"`
}
```


### `server/handles.BatchRenameReq`

源码：`server/handles/fsbatch.go:150`。

```go
type BatchRenameReq struct {
	SrcDir		string	`json:"src_dir"`
	RenameObjects	[]struct {
		SrcName	string	`json:"src_name"`
		NewName	string	`json:"new_name"`
	}	`json:"rename_objects"`
}
```


### `server/handles.CASInfoResp`

源码：`server/handles/torrent.go:361`。

```go
// CASInfoResp CAS 信息响应
type CASInfoResp struct {
	FileMD5		string	`json:"file_md5"`
	SliceMD5	string	`json:"slice_md5"`
	SliceSize	int64	`json:"slice_size"`
	Cloud		string	`json:"cloud"`
}
```


### `server/handles.CancelTranscodeJob.req`

源码：`server/handles/transcode.go:620`。

```go
req struct {
	JobID string `json:"job_id" binding:"required"`
}
```


### `server/handles.CreateMediaAppKey.req`

源码：`server/handles/media_extra.go:85`。

```go
req struct {
	Name string `json:"name"`
}
```


### `server/handles.CreateMediaScanPathReq`

源码：`server/handles/media.go:102`。

```go
// CreateMediaScanPathReq 创建扫描路径请求
type CreateMediaScanPathReq struct {
	MediaType	model.MediaType	`json:"media_type" binding:"required"`
	Name		string		`json:"name"`
	Path		string		`json:"path" binding:"required"`
	PathMerge	bool		`json:"path_merge"`
	MergeDepth	int		`json:"merge_depth"`
	TypeTag		string		`json:"type_tag"`
	ContentTags	string		`json:"content_tags"`
	EnableScrape	bool		`json:"enable_scrape"`
}
```


### `server/handles.DeleteAuthnLogin.DeleteAuthnReq`

源码：`server/handles/webauthn.go:206`。

```go
DeleteAuthnReq struct {
	ID string `json:"id"`
}
```


### `server/handles.DirReq`

源码：`server/handles/fsread.go:29`。

```go
type DirReq struct {
	Path		string	`json:"path" form:"path"`
	Password	string	`json:"password" form:"password"`
	ForceRoot	bool	`json:"force_root" form:"force_root"`
}
```


### `server/handles.DirResp`

源码：`server/handles/fsread.go:170`。

```go
type DirResp struct {
	Name		string		`json:"name"`
	Modified	time.Time	`json:"modified"`
}
```


### `server/handles.FsGetDirectUploadInfoReq`

源码：`server/handles/direct_upload.go:19`。

```go
type FsGetDirectUploadInfoReq struct {
	Path		string	`json:"path" form:"path"`
	FileName	string	`json:"file_name" form:"file_name"`
	FileSize	int64	`json:"file_size" form:"file_size"`
	ContentType	string	`json:"content_type" form:"content_type"`
	Tool		string	`json:"tool" form:"tool"`
}
```


### `server/handles.FsGetReq`

源码：`server/handles/fsread.go:251`。

```go
type FsGetReq struct {
	Path		string	`json:"path" form:"path"`
	Password	string	`json:"password" form:"password"`
}
```


### `server/handles.FsGetResp`

源码：`server/handles/fsread.go:256`。

```go
type FsGetResp struct {
	ObjResp
	RawURL		string		`json:"raw_url"`
	Readme		string		`json:"readme"`
	Header		string		`json:"header"`
	Provider	string		`json:"provider"`
	Related		[]ObjResp	`json:"related"`
}
```


### `server/handles.FsListResp`

源码：`server/handles/fsread.go:49`。

```go
type FsListResp struct {
	Content			[]ObjResp	`json:"content"`
	Total			int64		`json:"total"`
	Readme			string		`json:"readme"`
	Header			string		`json:"header"`
	Write			bool		`json:"write"`
	WriteContentBypass	bool		`json:"write_content_bypass"`
	Provider		string		`json:"provider"`
	DirectUploadTools	[]string	`json:"direct_upload_tools,omitempty"`
}
```


### `server/handles.FsOtherReq`

源码：`server/handles/fsread.go:398`。

```go
type FsOtherReq struct {
	model.FsOtherArgs
	Password	string	`json:"password" form:"password"`
}
```


### `server/handles.GenerateMediaStrm.req`

源码：`server/handles/media_extra.go:144`。

```go
req struct {
	MediaType	model.MediaType	`json:"media_type"`
	ScanPathID	uint		`json:"scan_path_id"`
}
```


### `server/handles.GenerateTorrentReq`

源码：`server/handles/torrent.go:631`。

```go
// GenerateTorrentReq 为指定路径的文件生成 torrent 请求
type GenerateTorrentReq struct {
	// Path 文件在 OpenList 中的路径
	Path	string	`json:"path" binding:"required"`
	// WithCAS 是否注入 CAS 扩展信息（仅天翼云需要）
	WithCAS	bool	`json:"with_cas"`
}
```


### `server/handles.GetAuthnCredentials.WebAuthnCredentials`

源码：`server/handles/webauthn.go:229`。

```go
WebAuthnCredentials struct {
	ID		[]byte	`json:"id"`
	FingerPrint	string	`json:"fingerprint"`
}
```


### `server/handles.InitSetupReq`

源码：`server/handles/setup.go:28`。

```go
// InitSetupReq 系统初始化请求体
type InitSetupReq struct {
	Username	string	`json:"username" binding:"required"`
	Password	string	`json:"password" binding:"required"`
	SiteTitle	string	`json:"site_title"`
}
```


### `server/handles.ListReq`

源码：`server/handles/fsread.go:22`。

```go
type ListReq struct {
	model.PageReq
	Path		string	`json:"path" form:"path"`
	Password	string	`json:"password" form:"password"`
	Refresh		bool	`json:"refresh"`
}
```


### `server/handles.LoginReq`

源码：`server/handles/auth.go:20`。

```go
type LoginReq struct {
	Username	string	`json:"username" binding:"required"`
	Password	string	`json:"password"`
	OtpCode		string	`json:"otp_code"`
}
```


### `server/handles.ManualScanReq`

源码：`server/handles/scan.go:9`。

```go
type ManualScanReq struct {
	Path	string	`json:"path"`
	Limit	float64	`json:"limit"`
}
```


### `server/handles.ManualScanResp`

源码：`server/handles/scan.go:36`。

```go
type ManualScanResp struct {
	ObjCount	uint64	`json:"obj_count"`
	IsDone		bool	`json:"is_done"`
}
```


### `server/handles.MediaExportData`

源码：`server/handles/media.go:1106`。

```go
// MediaExportData 导出数据结构
// 包含版本号便于后续兼容性升级；导出粒度由 scope 字段说明：
//   - "all"        ：所有类型的全部数据
//   - "media_type" ：单个媒体类型（video/music/image/book）的全部数据
//   - "scan_path"  ：单个扫描路径下的数据（仅 1 个路径 + 该路径下的条目）
type MediaExportData struct {
	Version		int			`json:"version"`
	ExportAt	time.Time		`json:"export_at"`
	Scope		string			`json:"scope"`
	MediaType	model.MediaType		`json:"media_type,omitempty"`
	ScanPaths	[]model.MediaScanPath	`json:"scan_paths"`
	Items		[]model.MediaItem	`json:"items"`
}
```


### `server/handles.MediaScrapeStatsResp`

源码：`server/handles/media.go:593`。

```go
// MediaScrapeStatsResp 刮削统计响应
// 包含 TMDB 视频刮削的累计指标，用于运维观测与命中率评估
type MediaScrapeStatsResp struct {
	TMDB scraper.TMDBStats `json:"tmdb"`
}
```


### `server/handles.MediaStatus`

源码：`server/handles/media_extra.go:21`。

```go
// MediaStatus 媒体库状态（前端据此决定是否渲染媒体库入口/侧栏）
type MediaStatus struct {
	Enabled		bool		`json:"enabled"`
	Types		[]string	`json:"types"`
	EmbyEnabled	bool		`json:"emby_enabled"`
	SubsonicEnabled	bool		`json:"subsonic_enabled"`
	EmbyBaseURL	string		`json:"emby_base_url"`
	SubsonicBaseURL	string		`json:"subsonic_base_url"`
	StrmPath	string		`json:"strm_path"`
	StrmOnScan	bool		`json:"strm_on_scan"`
	StrmSameDir	bool		`json:"strm_same_dir"`
	ThumbPath	string		`json:"thumb_path"`
	Version		string		`json:"version"`
	ProxyCover	bool		`json:"proxy_cover"`
}
```


### `server/handles.MkdirOrLinkReq`

源码：`server/handles/fsmanage.go:22`。

```go
type MkdirOrLinkReq struct {
	Path string `json:"path" form:"path"`
}
```


### `server/handles.MoveCopyReq`

源码：`server/handles/fsmanage.go:59`。

```go
type MoveCopyReq struct {
	SrcDir		string		`json:"src_dir"`
	DstDir		string		`json:"dst_dir"`
	Names		[]string	`json:"names"`
	Overwrite	bool		`json:"overwrite"`
	SkipExisting	bool		`json:"skip_existing"`
	Merge		bool		`json:"merge"`
	FollowSeed	bool		`json:"follow_seed"`
}
```


### `server/handles.MultipartInitResp`

源码：`server/handles/multipart.go:41`。

```go
type MultipartInitResp struct {
	multipart.SessionSnapshot
	Resumed	bool	`json:"resumed"`
}
```


### `server/handles.ObjResp`

源码：`server/handles/fsread.go:35`。

```go
type ObjResp struct {
	Name		string				`json:"name"`
	Size		int64				`json:"size"`
	IsDir		bool				`json:"is_dir"`
	Modified	time.Time			`json:"modified"`
	Created		time.Time			`json:"created"`
	Sign		string				`json:"sign"`
	Thumb		string				`json:"thumb"`
	Type		int				`json:"type"`
	HashInfoStr	string				`json:"hashinfo"`
	HashInfo	map[*utils.HashType]string	`json:"hash_info"`
	MountDetails	*model.StorageDetails		`json:"mount_details,omitempty"`
}
```


### `server/handles.ParseTorrentReq`

源码：`server/handles/torrent.go:329`。

```go
// ParseTorrentReq 解析 torrent 文件请求
type ParseTorrentReq struct {
	// TorrentData Base64 编码的 torrent 文件内容
	TorrentData string `json:"torrent_data" binding:"required"`
}
```


### `server/handles.ParseTorrentResp`

源码：`server/handles/torrent.go:335`。

```go
// ParseTorrentResp 解析 torrent 文件响应
type ParseTorrentResp struct {
	// Name 种子名称
	Name	string	`json:"name"`
	// TotalSize 总大小
	TotalSize	int64	`json:"total_size"`
	// PieceLength 分片大小
	PieceLength	int64	`json:"piece_length"`
	// PieceCount 分片数量
	PieceCount	int	`json:"piece_count"`
	// InfoHash info_hash（十六进制）
	InfoHash	string	`json:"info_hash"`
	// Files 文件列表（多文件模式）
	Files	[]TorrentFileInfo	`json:"files"`
	// HasCAS 是否包含 CAS 扩展信息
	HasCAS	bool	`json:"has_cas"`
	// CAS CAS 扩展信息
	CAS	*CASInfoResp	`json:"cas,omitempty"`
}
```


### `server/handles.RecursiveMoveReq`

源码：`server/handles/fsbatch.go:19`。

```go
type RecursiveMoveReq struct {
	SrcDir		string	`json:"src_dir"`
	DstDir		string	`json:"dst_dir"`
	ConflictPolicy	string	`json:"conflict_policy"`
}
```


### `server/handles.RegexRenameReq`

源码：`server/handles/fsbatch.go:210`。

```go
type RegexRenameReq struct {
	SrcDir		string	`json:"src_dir"`
	SrcNameRegex	string	`json:"src_name_regex"`
	NewNameRegex	string	`json:"new_name_regex"`
}
```


### `server/handles.RemoveEmptyDirectoryReq`

源码：`server/handles/fsmanage.go:506`。

```go
type RemoveEmptyDirectoryReq struct {
	SrcDir string `json:"src_dir"`
}
```


### `server/handles.RemoveReq`

源码：`server/handles/fsmanage.go:369`。

```go
type RemoveReq struct {
	Dir		string		`json:"dir"`
	Names		[]string	`json:"names"`
	FollowSeed	bool		`json:"follow_seed"`
}
```


### `server/handles.RenameReq`

源码：`server/handles/fsmanage.go:304`。

```go
type RenameReq struct {
	Path		string	`json:"path"`
	Name		string	`json:"name"`
	Overwrite	bool	`json:"overwrite"`
	FollowSeed	bool	`json:"follow_seed"`
}
```


### `server/handles.SSHKeyAddReq`

源码：`server/handles/sshkey.go:14`。

```go
type SSHKeyAddReq struct {
	Title	string	`json:"title" binding:"required"`
	Key	string	`json:"key" binding:"required"`
}
```


### `server/handles.SaveMediaConfigReq`

源码：`server/handles/media.go:58`。

```go
// SaveMediaConfigReq 保存配置请求
type SaveMediaConfigReq struct {
	MediaType	model.MediaType	`json:"media_type" binding:"required"`
	Enabled		bool		`json:"enabled"`
	ScraperConfig	*string		`json:"scraper_config"`
}
```


### `server/handles.ScanMediaReq`

源码：`server/handles/media.go:543`。

```go
// ScanMediaReq 扫描请求
type ScanMediaReq struct {
	MediaType	model.MediaType	`json:"media_type" binding:"required"`
	ScanPathID	uint		`json:"scan_path_id"`	// 0 表示扫描全部路径
}
```


### `server/handles.ScrapeMediaReq`

源码：`server/handles/media.go:612`。

```go
// ScrapeMediaReq 刮削请求
type ScrapeMediaReq struct {
	MediaType	model.MediaType	`json:"media_type" binding:"required"`
	ItemID		uint		`json:"item_id"`	// 0 表示刮削全部未刮削的
}
```


### `server/handles.SearchReq`

源码：`server/handles/search.go:17`。

```go
type SearchReq struct {
	model.SearchReq
	Password	string	`json:"password"`
}
```


### `server/handles.SeedCapabilityReq`

源码：`server/handles/torrent.go:817`。

```go
// SeedCapabilityReq supports source preflight and destination import planning.
type SeedCapabilityReq struct {
	SeedDataReq
	Paths		[]string	`json:"paths"`
	Path		string		`json:"path"`
	Override	string		`json:"policy"`
}
```


### `server/handles.SeedConvertReq`

源码：`server/handles/torrent.go:762`。

```go
// SeedConvertReq requests a container conversion without changing metadata.
type SeedConvertReq struct {
	SeedDataReq
	TargetFormat	string	`json:"target_format"`
	// Formats 支持一次转换到多个格式；为空时回落到 TargetFormat / Format。
	Formats	[]string	`json:"formats"`
	Path	string		`json:"path"`
	// ComputeMissing 为 true 时，缺少的哈希会从 FileBasePath 下的实际文件重新计算，
	// 而不是直接返回「不可转换」。
	ComputeMissing	bool	`json:"compute_missing"`
	// FileBasePath 是种子内相对路径的根目录（VFS 路径）。
	FileBasePath	string	`json:"file_base_path"`
	// Comment 非 nil 时覆盖种子的整体注释（转换时一并保存）。
	Comment		*string		`json:"comment"`
	SelectedFiles	[]int		`json:"selected_files"`
	HashMatrix	*SeedHashMatrix	`json:"hash_matrix"`
}
```


### `server/handles.SeedDataReq`

源码：`server/handles/torrent.go:755`。

```go
// SeedDataReq carries a bounded base64-encoded seed document.
// SeedData is intentionally NOT bound with `required` here: some requests that
// embed SeedDataReq (e.g. SeedCapabilityReq preflight) accept `paths` instead of
// `seed_data`. Required-ness is enforced inside decodeSeedData for the handlers
// that actually consume a seed document.
type SeedDataReq struct {
	SeedData	string	`json:"seed_data"`
	Format		string	`json:"format"`
	FileName	string	`json:"file_name"`
}
```


### `server/handles.SeedGenerateReq`

源码：`server/handles/torrent.go:793`。

```go
// SeedGenerateReq generates one or more seed containers for existing files.
type SeedGenerateReq struct {
	Paths		[]string		`json:"paths" binding:"required"`
	Format		string			`json:"format"`
	Formats		[]string		`json:"formats"`
	Name		string			`json:"name"`
	Comment		string			`json:"comment"`
	FileComments	map[string]string	`json:"file_comments"`
	HashMatrix	SeedHashMatrix		`json:"hash_matrix"`
	PieceSize	int64			`json:"piece_size"`
	Trackers	[]string		`json:"trackers"`
	Channels	[]torrent.SeedChannel	`json:"channels"`
	OutputPath	string			`json:"output_path"`
	SavePath	string			`json:"save_path"`
	// PreserveStructure 目录展开后保留目录结构；默认 false（拍平成文件名）
	PreserveStructure	bool	`json:"preserve_structure"`
	// Split 每个文件生成一个种子；默认 false（全部合并成一个）
	Split			bool		`json:"split"`
	IncludeShare		bool		`json:"include_share"`
	IncludeDirectSource	bool		`json:"include_direct_source"`
	ShareFiles		[]string	`json:"share_files"`
	DirectFiles		[]string	`json:"direct_files"`
}
```


### `server/handles.SeedHashMatrix`

源码：`server/handles/torrent.go:786`。

```go
// SeedHashMatrix controls the optional hash metadata stored in a seed.
type SeedHashMatrix struct {
	MD5	SeedHashSelection	`json:"md5"`
	SHA1	SeedHashSelection	`json:"sha1"`
	SHA256	SeedHashSelection	`json:"sha256"`
}
```


### `server/handles.SeedHashSelection`

源码：`server/handles/torrent.go:780`。

```go
// SeedHashSelection controls whole-file and piece hash inclusion.
type SeedHashSelection struct {
	Whole	bool	`json:"whole"`
	Pieces	bool	`json:"pieces"`
}
```


### `server/handles.SeedQuickSaveReq`

源码：`server/handles/torrent.go:855`。

```go
// SeedQuickSaveReq imports selected seed files using rapid upload or an existing source URL.
type SeedQuickSaveReq struct {
	SeedDataReq
	Path		string		`json:"path" binding:"required"`
	Files		[]string	`json:"files"`
	SelectedFiles	[]int		`json:"selected_files"`
	Tool		string		`json:"tool"`
	DeletePolicy	string		`json:"delete_policy"`
	Overwrite	bool		`json:"overwrite"`
	TransitPath	string		`json:"transit_path"`
	UpdateChannel	bool		`json:"update_channel"`
	Options		map[string]any	`json:"options"`
}
```


### `server/handles.SeedRecalcFile`

源码：`server/handles/torrent.go:831`。

```go
// SeedRecalcFile maps one seed file to a server-side path for re-hashing.
type SeedRecalcFile struct {
	Path		string	`json:"path"`		// seed file relative path
	SourcePath	string	`json:"source_path"`	// server-side readable file path
}
```


### `server/handles.SeedUpdateReq`

源码：`server/handles/torrent.go:837`。

```go
// SeedUpdateReq edits metadata and optionally recalculates hashes.
type SeedUpdateReq struct {
	SeedDataReq
	Comment		*string				`json:"comment"`
	Trackers	[]string			`json:"trackers"`
	Channels	[]torrent.SeedChannel		`json:"channels"`
	FileComments	map[string]string		`json:"file_comments"`
	FileSources	map[string][]torrent.SeedSource	`json:"file_sources"`
	RemoveFiles	[]string			`json:"remove_files"`
	Recalculate	bool				`json:"recalculate"`
	RecalcFiles	[]SeedRecalcFile		`json:"recalc_files"`
	HashMatrix	SeedHashMatrix			`json:"hash_matrix"`
	PieceSize	int64				`json:"piece_size"`
	OutputPath	string				`json:"output_path"`
	SavePath	string				`json:"save_path"`
	Options		map[string]any			`json:"options"`
}
```


### `server/handles.Set115OpenReq`

源码：`server/handles/offline_download.go:141`。

```go
type Set115OpenReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.Set115Req`

源码：`server/handles/offline_download.go:119`。

```go
type Set115Req struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.Set123OpenReq`

源码：`server/handles/offline_download.go:185`。

```go
type Set123OpenReq struct {
	TempDir		string	`json:"temp_dir" form:"temp_dir"`
	CallbackUrl	string	`json:"callback_url" form:"callback_url"`
}
```


### `server/handles.Set123PanReq`

源码：`server/handles/offline_download.go:163`。

```go
type Set123PanReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetAria2Req`

源码：`server/handles/offline_download.go:55`。

```go
type SetAria2Req struct {
	Uri	string	`json:"uri" form:"uri"`
	Secret	string	`json:"secret" form:"secret"`
}
```


### `server/handles.SetGuangYaPanReq`

源码：`server/handles/offline_download.go:297`。

```go
type SetGuangYaPanReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetPikPakReq`

源码：`server/handles/offline_download.go:209`。

```go
type SetPikPakReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetQbittorrentReq`

源码：`server/handles/offline_download.go:77`。

```go
type SetQbittorrentReq struct {
	Url		string	`json:"url" form:"url"`
	Seedtime	string	`json:"seedtime" form:"seedtime"`
}
```


### `server/handles.SetThunderBrowserReq`

源码：`server/handles/offline_download.go:275`。

```go
type SetThunderBrowserReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetThunderReq`

源码：`server/handles/offline_download.go:231`。

```go
type SetThunderReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetThunderXReq`

源码：`server/handles/offline_download.go:253`。

```go
type SetThunderXReq struct {
	TempDir string `json:"temp_dir" form:"temp_dir"`
}
```


### `server/handles.SetTransmissionReq`

源码：`server/handles/offline_download.go:98`。

```go
type SetTransmissionReq struct {
	Uri		string	`json:"uri" form:"uri"`
	Seedtime	string	`json:"seedtime" form:"seedtime"`
}
```


### `server/handles.SharingResp`

源码：`server/handles/sharing.go:351`。

```go
type SharingResp struct {
	*model.Sharing
	CreatorName	string	`json:"creator"`
	CreatorRole	int	`json:"creator_role"`
}
```


### `server/handles.StartMediaScrape.sc`

源码：`server/handles/media.go:645`。

```go
sc struct {
	StoreThumbnail	string	`json:"store_thumbnail"`
	ThumbnailMode	string	`json:"thumbnail_mode"`
	ThumbnailPath	string	`json:"thumbnail_path"`
}
```


### `server/handles.StorageResp`

源码：`server/handles/storage.go:21`。

```go
type StorageResp struct {
	model.Storage
	MountDetails	*model.StorageDetails	`json:"mount_details,omitempty"`
}
```


### `server/handles.TaskInfo`

源码：`server/handles/task.go:20`。

```go
type TaskInfo struct {
	*tool.Open115TaskInfo
	ID		string		`json:"id"`
	Name		string		`json:"name"`
	Creator		string		`json:"creator"`
	CreatorRole	int		`json:"creator_role"`
	State		tache.State	`json:"state"`
	Status		string		`json:"status"`
	Progress	float64		`json:"progress"`
	StartTime	*time.Time	`json:"start_time"`
	EndTime		*time.Time	`json:"end_time"`
	TotalBytes	int64		`json:"total_bytes"`
	Error		string		`json:"error"`
}
```


### `server/handles.TorrentFileInfo`

源码：`server/handles/torrent.go:355`。

```go
// TorrentFileInfo torrent 中的文件信息
type TorrentFileInfo struct {
	Path	string	`json:"path"`
	Size	int64	`json:"size"`
}
```


### `server/handles.TorrentRapidUploadReq`

源码：`server/handles/torrent.go:439`。

```go
// TorrentRapidUploadReq 从 torrent 秒传请求
type TorrentRapidUploadReq struct {
	// TorrentData Base64 编码的 torrent 文件内容
	TorrentData	string	`json:"torrent_data" binding:"required"`
	// Path 目标路径
	Path	string	`json:"path" binding:"required"`
}
```


### `server/handles.TranscodePlay.req`

源码：`server/handles/transcode.go:86`。

```go
req struct {
	Path string `json:"path" binding:"required"`
}
```


### `server/handles.UpdateIndexReq`

源码：`server/handles/index.go:16`。

```go
type UpdateIndexReq struct {
	Paths		[]string	`json:"paths"`
	MaxDepth	int		`json:"max_depth"`
}
```


### `server/handles.UpdateMediaAlbumReq`

源码：`server/handles/media_album.go:71`。

```go
// UpdateMediaAlbumReq 专辑级批量修改
//
// 专辑是「按 album_name + album_artist 聚合出来的虚拟分组」，没有独立表，
// 因此这里把改动一次性写到该专辑下的所有条目（封面 / 简介 / 年份 / 类型 / 名称）。
type UpdateMediaAlbumReq struct {
	// 定位旧专辑
	AlbumName	*string	`json:"album_name"`
	AlbumArtist	*string	`json:"album_artist"`
	ScanPathID	uint	`json:"scan_path_id"`
	// 新值
	NewAlbumName	*string	`json:"new_album_name"`
	NewAlbumArtist	*string	`json:"new_album_artist"`
	Cover		*string	`json:"cover"`
	ReleaseDate	*string	`json:"release_date"`
	Genre		*string	`json:"genre"`
	Plot		*string	`json:"plot"`
	Description	*string	`json:"description"`
}
```


### `server/handles.UpdateMediaItemReq`

源码：`server/handles/media.go:279`。

```go
// UpdateMediaItemReq 更新媒体条目请求。
//
// 所有字段都是指针：只有请求里出现过的键才会被写入，未出现的保持原值。
// 之前用值类型是「全量覆盖」语义，任何只传部分字段的调用（例如只切 hidden）
// 都会把 cover/plot 等清空，接口层面太脆弱。
type UpdateMediaItemReq struct {
	ID	uint	`json:"id" binding:"required"`

	ScrapedName	*string		`json:"scraped_name"`
	Description	*string		`json:"description"`
	Cover		*string		`json:"cover"`
	ReleaseDate	*string		`json:"release_date"`
	Rating		*float32	`json:"rating"`
	Genre		*string		`json:"genre"`
	Authors		*string		`json:"authors"`
	Plot		*string		`json:"plot"`
	Reviews		*string		`json:"reviews"`
	AlbumName	*string		`json:"album_name"`
	AlbumArtist	*string		`json:"album_artist"`
	Publisher	*string		`json:"publisher"`
	ISBN		*string		`json:"isbn"`
	Hidden		*bool		`json:"hidden"`

	// 合集 / 视频 / 音乐的扩展可编辑项
	Episodes	*string	`json:"episodes"`	// 选集 JSON：[{file_name,index,title,season}]
	VideoType	*string	`json:"video_type"`	// movie / tv / 空
	Season		*int	`json:"season"`		// 季
	Episode		*int	`json:"episode"`	// 集
	TrackNumber	*int	`json:"track_number"`	// 曲目号
	Duration	*int	`json:"duration"`	// 时长（秒）
	ExternalID	*string	`json:"external_id"`	// 外部 ID（tmdb:xxx / discogs:xxx）
}
```


### `server/handles.UpdateMediaScanPathReq`

源码：`server/handles/media.go:157`。

```go
// UpdateMediaScanPathReq 更新扫描路径请求
type UpdateMediaScanPathReq struct {
	ID		uint	`json:"id" binding:"required"`
	Name		string	`json:"name"`
	Path		string	`json:"path"`
	PathMerge	bool	`json:"path_merge"`
	MergeDepth	int	`json:"merge_depth"`
	TypeTag		string	`json:"type_tag"`
	ContentTags	string	`json:"content_tags"`
	EnableScrape	bool	`json:"enable_scrape"`
}
```


### `server/handles.UpdateSharingReq`

源码：`server/handles/sharing.go:404`。

```go
type UpdateSharingReq struct {
	Files		[]string	`json:"files"`
	Expires		*time.Time	`json:"expires"`
	Pwd		string		`json:"pwd"`
	MaxAccessed	int		`json:"max_accessed"`
	Disabled	bool		`json:"disabled"`
	Remark		string		`json:"remark"`
	Readme		string		`json:"readme"`
	Header		string		`json:"header"`
	model.Sort
	CreatorName	string	`json:"creator"`
	Accessed	int	`json:"accessed"`
	ID		string	`json:"id"`
	NewID		string	`json:"new_id"`
}
```


### `server/handles.UserResp`

源码：`server/handles/auth.go:89`。

```go
type UserResp struct {
	model.User
	Otp	bool	`json:"otp"`
}
```


### `server/handles.Verify2FAReq`

源码：`server/handles/auth.go:178`。

```go
type Verify2FAReq struct {
	Code	string	`json:"code" binding:"required"`
	Secret	string	`json:"secret" binding:"required"`
}
```


### `server/handles.addOfflineDownloadResult`

源码：`server/handles/offline_download.go:392`。

```go
type addOfflineDownloadResult struct {
	Index	int	`json:"index"`
	TaskID	string	`json:"task_id,omitempty"`
	Error	string	`json:"error,omitempty"`
}
```


### `server/handles.cachedExif`

源码：`server/handles/media_exif.go:58`。

```go
type cachedExif struct {
	size	int64
	updated	time.Time
	data	*mediaExifResp
}
```


### `server/handles.coverBlob`

源码：`server/handles/media_export.go:182`。

```go
type coverBlob struct {
	data	[]byte
	ext	string
}
```


### `server/handles.detailWithIndex`

源码：`server/handles/storage.go:26`。

```go
type detailWithIndex struct {
	idx	int
	val	*model.StorageDetails
}
```


### `server/handles.favoriteReq`

源码：`server/handles/media_user.go:132`。

```go
// SetMediaFavorite 收藏 / 取消收藏
type favoriteReq struct {
	ItemID		uint	`json:"item_id"`
	EpisodeIndex	int	`json:"episode_index"`
	Favorite	bool	`json:"favorite"`
}
```


### `server/handles.mediaAnnotationPayload`

源码：`server/handles/media_reader.go:68`。

```go
type mediaAnnotationPayload struct {
	RemoteID	string	`json:"remote_id"`
	Anchor		string	`json:"anchor"`
}
```


### `server/handles.mediaAnnotationSyncReq`

源码：`server/handles/media_reader.go:73`。

```go
type mediaAnnotationSyncReq struct {
	ItemID	uint				`json:"item_id"`
	Upserts	[]mediaAnnotationPayload	`json:"upserts"`
	Deletes	[]string			`json:"deletes"`
}
```


### `server/handles.mediaContextReader`

源码：`server/handles/media_export.go:550`。

```go
type mediaContextReader struct {
	ctx	context.Context
	r	io.Reader
}
```


### `server/handles.mediaContextWriter`

源码：`server/handles/media_export.go:300`。

```go
type mediaContextWriter struct {
	ctx	context.Context
	w	io.Writer
}
```


### `server/handles.mediaExifResp`

源码：`server/handles/media_exif.go:33`。

```go
// mediaExifResp 图片 EXIF 摘要。字段都省略空值，前端按有无渲染。
type mediaExifResp struct {
	Available	bool	`json:"available"`
	// 基本信息
	Format	string	`json:"format,omitempty"`
	Width	int	`json:"width,omitempty"`
	Height	int	`json:"height,omitempty"`
	// 拍摄设备
	Camera		string	`json:"camera,omitempty"`
	Lens		string	`json:"lens,omitempty"`
	Software	string	`json:"software,omitempty"`
	// 拍摄参数
	Exposure	string	`json:"exposure,omitempty"`
	Aperture	string	`json:"aperture,omitempty"`
	ISO		string	`json:"iso,omitempty"`
	Focal		string	`json:"focal_length,omitempty"`
	TakenAt		string	`json:"taken_at,omitempty"`
	// 位置
	Latitude	*float64	`json:"latitude,omitempty"`
	Longitude	*float64	`json:"longitude,omitempty"`
}
```


### `server/handles.mediaImportLimits`

源码：`server/handles/media_export.go:319`。

```go
type mediaImportLimits struct {
	entries		int
	jsonBytes	int64
	coverBytes	int64
}
```


### `server/handles.mediaImportedCover`

源码：`server/handles/media_export.go:325`。

```go
type mediaImportedCover struct {
	path	string
	cover	string
}
```


### `server/handles.saveProgressReq`

源码：`server/handles/media_user.go:74`。

```go
// SaveMediaProgress 上报播放进度
type saveProgressReq struct {
	ItemID		uint	`json:"item_id"`
	EpisodeIndex	int	`json:"episode_index"`
	PositionSec	float64	`json:"position"`
	DurationSec	float64	`json:"duration"`
	Played		bool	`json:"played"`
}
```


### `server/handles.ssoBindingClaims`

源码：`server/handles/ssologin.go:38`。

```go
type ssoBindingClaims struct {
	Purpose		string	`json:"purpose"`
	Method		string	`json:"method"`
	SessionDigest	string	`json:"session_digest"`
	SsoID		string	`json:"sso_id,omitempty"`
	jwt.RegisteredClaims
}
```


### `server/handles.taskDeleteResult`

源码：`server/handles/task_delete.go:14`。

```go
type taskDeleteResult struct {
	TaskID		string	`json:"task_id"`
	LocalDeleted	bool	`json:"local_deleted"`
	CloudState	string	`json:"cloud_state"`
	Code		int	`json:"code"`
	Reason		string	`json:"reason,omitempty"`
	Error		string	`json:"error,omitempty"`
}
```


## 各处理函数的参数、响应与实现依据

多个方法／路径可复用同一处理函数。下面列出处理函数直接读取的参数和响应构造；其调用的本地辅助函数也已收录。`common.SuccessResp(c, X)` 中的 `X` 是 `data`；错误的附加数据见 `ErrorWithDataResp`。变量或函数表达式不是固定 JSON 示例。完整函数源码在 OpenAPI 顶层 `x-openlist-contracts` 中，读取该文件可继续追踪条件响应和辅助函数。


### `internal/message.GetHandle`

源码：`internal/message/http.go:20`。

```go
common.SuccessResp(c, message)
common.ErrorStrResp(c, "no message", 404)
```


### `internal/message.SendHandle`

源码：`internal/message/http.go:29`。

请求绑定：`ShouldBind` → `internal/message.Req`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c)
common.ErrorStrResp(c, "nowhere needed", 500)
```


### `server/handles.AddMyPublicKey`

源码：`server/handles/sshkey.go:19`。

请求绑定：`ShouldBind` → `server/handles.SSHKeyAddReq`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "request invalid", 400)
common.ErrorStrResp(c, "provided key invalid", 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.AddOfflineDownload`

源码：`server/handles/offline_download.go:331`。

请求绑定：`ShouldBind` → `server/handles.AddOfflineDownloadReq`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "permission denied", 403)
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{
	"tasks": getTaskInfos(tasks),
})
```

本地后续调用：`server/handles.add115OfflineDownloads`, `server/handles.getTaskInfos`。


### `server/handles.ArchiveExtensions`

源码：`server/handles/archive.go:413`。

```go
common.SuccessResp(c, ext)
```


### `server/handles.BeginAuthnLogin`

源码：`server/handles/webauthn.go:21`。

直接参数：query `username`。

```go
common.ErrorStrResp(c, "WebAuthn is not enabled", 403)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, gin.H{
	"options":	options,
	"session":	val,
})
```


### `server/handles.BeginAuthnRegistration`

源码：`server/handles/webauthn.go:122`。

```go
common.ErrorStrResp(c, "WebAuthn is not enabled", 403)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, gin.H{
	"options":	options,
	"session":	val,
})
```


### `server/handles.BuildIndex`

源码：`server/handles/index.go:22`。

```go
common.ErrorStrResp(c, "index is running", 400)
common.SuccessResp(c)
```


### `server/handles.CanUserAccessItem`

源码：`server/handles/media_gate.go:131`。

本地后续调用：`server/handles.CanUserAccessPath`, `server/handles.MediaItemFilePath`, `server/handles.MediaTypeEnabled`。


### `server/handles.CanUserAccessPath`

源码：`server/handles/media_gate.go:66`。


### `server/handles.Cancel2FAById`

源码：`server/handles/user.go:117`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.CancelTranscodeJob`

源码：`server/handles/transcode.go:619`。

请求绑定：`ShouldBindJSON` → `server/handles.CancelTranscodeJob.req`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c)
```


### `server/handles.ClearIndex`

源码：`server/handles/index.go:108`。

```go
common.ErrorStrResp(c, "index is running", 400)
common.SuccessResp(c)
```


### `server/handles.ClearMediaDB`

源码：`server/handles/media.go:449`。

直接参数：query `media_type`。

```go
common.ErrorStrResp(c, "media_type 不能为空", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.ClearMediaScanPathDB`

源码：`server/handles/media.go:210`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.ClearMediaScrape`

源码：`server/handles/media.go:464`。

直接参数：query `media_type`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{"affected": affected})
```


### `server/handles.ConvertSeed`

源码：`server/handles/torrent.go:963`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedConvertReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "target format is required", 400)
common.SuccessResp(c, gin.H{"convertible": false, "format": target, "diagnostics": diags})
common.ErrorResp(c, err, 403)
common.ErrorResp(c, metaErr, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, convertErr, code)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, result)
```

本地后续调用：`server/handles.convertSeedToFormat`, `server/handles.decodeSeedData`, `server/handles.seedConvertTargets`, `server/handles.seedSaveErrorCode`, `server/handles.selectSeedConvertFiles`。


### `server/handles.CreateMediaAppKey`

源码：`server/handles/media_extra.go:79`。

请求绑定：`ShouldBindJSON` → `server/handles.CreateMediaAppKey.req`。

```go
common.ErrorStrResp(c, "未登录", 401)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{
	"id":		key.ID,
	"name":		key.Name,
	"prefix":	key.Prefix,
	"key":		plain,
})
```

本地后续调用：`server/handles.mediaUser`。


### `server/handles.CreateMediaScanPath`

源码：`server/handles/media.go:126`。

请求绑定：`ShouldBindJSON` → `server/handles.CreateMediaScanPathReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, sp)
```

本地后续调用：`server/handles.clampMergeDepth`。


### `server/handles.CreateMeta`

源码：`server/handles/meta.go:35`。

请求绑定：`ShouldBind` → `internal/model.Meta`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, fmt.Sprintf("%s is illegal: %s", r, err.Error()), 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```

本地后续调用：`server/handles.validHide`。


### `server/handles.CreateSharing`

源码：`server/handles/sharing.go:510`。

请求绑定：`ShouldBind` → `server/handles.UpdateSharingReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "must add at least 1 object", 400)
common.ErrorStrResp(c, "no such a user", 400)
common.ErrorStrResp(c, "permission denied", 403)
common.ErrorStrResp(c, fmt.Sprintf("permission denied to share path [%s]", s), 500)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, SharingResp{
	Sharing:	s,
	CreatorName:	s.Creator.Username,
	CreatorRole:	s.Creator.Role,
})
```

本地后续调用：`server/handles.validateSharingID`。


### `server/handles.CreateStorage`

源码：`server/handles/storage.go:96`。

请求绑定：`ShouldBind` → `internal/model.Storage`。

```go
common.ErrorResp(c, err, 400)
common.ErrorWithDataResp(c, err, 500, gin.H{
	"id": id,
}, true)
common.SuccessResp(c, gin.H{
	"id": id,
})
```


### `server/handles.CreateUser`

源码：`server/handles/user.go:32`。

请求绑定：`ShouldBind` → `internal/model.User`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "admin or guest user can not be created", 400, true)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.CurrentUser`

源码：`server/handles/auth.go:96`。

```go
common.SuccessResp(c, userResp)
```


### `server/handles.DefaultSettings`

源码：`server/handles/setting.go:96`。

直接参数：query `group`; query `groups`。

```go
common.SuccessResp(c, settings)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, resultItems)
```


### `server/handles.DelUserCache`

源码：`server/handles/user.go:131`。

直接参数：query `username`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DeleteAuthnLogin`

源码：`server/handles/webauthn.go:204`。

请求绑定：`ShouldBind` → `server/handles.DeleteAuthnLogin.DeleteAuthnReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "Deleted Successfully")
```


### `server/handles.DeleteInvalidMedia`

源码：`server/handles/media.go:491`。

直接参数：query `media_type`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{
	"checked":	len(items),
	"deleted":	len(invalidIDs),
	"skipped":	skipped,
})
```


### `server/handles.DeleteMediaAppKey`

源码：`server/handles/media_extra.go:122`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "未登录", 401)
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, nil)
```

本地后续调用：`server/handles.mediaUser`。


### `server/handles.DeleteMediaItemAdmin`

源码：`server/handles/media.go:434`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DeleteMediaScanPath`

源码：`server/handles/media.go:195`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DeleteMeta`

源码：`server/handles/meta.go:82`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.DeleteMyPublicKey`

源码：`server/handles/sshkey.go:59`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "id format invalid", 400)
common.ErrorStrResp(c, "failed to get public key", 404)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.DeletePublicKey`

源码：`server/handles/sshkey.go:97`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "id format invalid", 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.DeleteSetting`

源码：`server/handles/setting.go:136`。

直接参数：query `key`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DeleteSharing`

源码：`server/handles/sharing.go:579`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DeleteStorage`

源码：`server/handles/storage.go:126`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.DeleteUser`

源码：`server/handles/user.go:88`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.DiagnoseSeed`

源码：`server/handles/torrent.go:1233`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedDataReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, seedDiagnostics(seed))
```

本地后续调用：`server/handles.decodeSeedData`, `server/handles.seedDiagnostics`。


### `server/handles.DisableStorage`

源码：`server/handles/storage.go:140`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.EnableStorage`

源码：`server/handles/storage.go:154`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.ExportMediaDB`

源码：`server/handles/media.go:1124`。

直接参数：query `media_type`; query `scan_path_id`。

```go
common.ErrorStrResp(c, "无效的 scan_path_id", 400)
common.ErrorResp(c, err, 404)
common.ErrorResp(c, err, 500)
c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
c.Header("Content-Type", "application/zip")
```

本地后续调用：`server/handles.buildExportFilename`, `server/handles.writeMediaExportZip`。


### `server/handles.FinishAuthnLogin`

源码：`server/handles/webauthn.go:62`。

直接参数：header `session`; query `username`。

```go
common.ErrorStrResp(c, "WebAuthn is not enabled", 403)
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 400, true)
common.SuccessResp(c, gin.H{"token": token})
```


### `server/handles.FinishAuthnRegistration`

源码：`server/handles/webauthn.go:158`。

直接参数：header `Session`。

```go
common.ErrorStrResp(c, "WebAuthn is not enabled", 403)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "Registered Successfully")
```


### `server/handles.FsArchiveDecompress`

源码：`server/handles/archive.go:238`。

请求绑定：`ShouldBind` → `server/handles.ArchiveDecompressReq`。

直接参数：query `type`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, e, 202)
common.ErrorResp(c, e, 500)
common.SuccessResp(c, gin.H{
	"task": getTaskInfos(tasks),
})
```

本地后续调用：`server/handles.getTaskInfos`。


### `server/handles.FsArchiveList`

源码：`server/handles/archive.go:176`。

直接参数：query `type`。

```go
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.ErrorResp(c, err, 202)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{
	Content:	ret,
	Total:		int64(total),
})
```

本地后续调用：`server/handles.pagination`, `server/handles.toObjsRespWithoutSignAndThumb`。


### `server/handles.FsArchiveListSplit`

源码：`server/handles/archive.go:156`。

请求绑定：`ShouldBind` → `server/handles.ArchiveListReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
```

本地后续调用：`server/handles.FsArchiveList`, `server/handles.SharingArchiveList`。


### `server/handles.FsArchiveMeta`

源码：`server/handles/archive.go:93`。

直接参数：query `type`。

```go
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.ErrorResp(c, err, 202)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, ArchiveMetaResp{
	Comment:	ret.GetComment(),
	IsEncrypted:	ret.IsEncrypted(),
	Content:	toContentResp(ret.GetTree()),
	Sort:		ret.Sort,
	RawURL:		fmt.Sprintf("%s%s%s", common.GetApiUrl(c), api, utils.EncodePath(reqPath, true)),
	Sign:		s,
})
```

本地后续调用：`server/handles.isEncrypt`, `server/handles.toContentResp`。


### `server/handles.FsArchiveMetaSplit`

源码：`server/handles/archive.go:74`。

请求绑定：`ShouldBind` → `server/handles.ArchiveMetaReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
```

本地后续调用：`server/handles.FsArchiveMeta`, `server/handles.SharingArchiveMeta`。


### `server/handles.FsBatchRename`

源码：`server/handles/fsbatch.go:159`。

请求绑定：`ShouldBind` → `server/handles.BatchRenameReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.checkRelativePath`。


### `server/handles.FsCopy`

源码：`server/handles/fsmanage.go:184`。

请求绑定：`ShouldBind` → `server/handles.MoveCopyReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Empty file names", 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, fmt.Sprintf("invalid file name [%s]", name), 400)
common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", name), 403)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, followErr, 500)
common.SuccessResp(c, gin.H{
	"message":	fmt.Sprintf("Successfully created %d copy task(s)", len(addedTasks)),
	"tasks":	getTaskInfos(addedTasks),
})
common.SuccessResp(c, gin.H{
	"message": "Copy operations completed immediately",
})
```

本地后续调用：`server/handles.checkRelativePath`, `server/handles.followSeedTransfer`, `server/handles.getTaskInfos`。


### `server/handles.FsDirs`

源码：`server/handles/fsread.go:130`。

请求绑定：`ShouldBind` → `server/handles.DirReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Permission denied", 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, dirs)
```

本地后续调用：`server/handles.filterDirs`。


### `server/handles.FsForm`

源码：`server/handles/fsup.go:167`。

直接参数：header `File-Path`; header `As-Task`; header `Overwrite`; file `file`; header `X-File-Md5`; header `X-File-Sha1`; header `X-File-Sha256`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorStrResp(c, "file exists", 403)
common.ErrorStrResp(c, "Current storage doesn't support upload", 405)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, errs.IgnoredSystemFile.Error(), 403)
common.ErrorStrResp(c, err.Error(), 400)
common.ErrorStrResp(c, "seed sidecar generation requires synchronous upload", 400)
common.SuccessResp(c)
common.SuccessResp(c, gin.H{
	"task": getTaskInfo(t),
})
```

本地后续调用：`server/handles.getLastModified`, `server/handles.getTaskInfo`, `server/handles.shouldGenerateUploadSeed`, `server/handles.shouldIgnoreSystemFile`, `server/handles.uploadSeedSliceSize`, `server/handles.validateUploadSeedRequest`, `server/handles.writeUploadSeedSidecar`。


### `server/handles.FsGet`

源码：`server/handles/fsread.go:284`。

```go
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, FsGetResp{
	ObjResp: ObjResp{
		Name:		obj.GetName(),
		Size:		obj.GetSize(),
		IsDir:		obj.IsDir(),
		Modified:	obj.ModTime(),
		Created:	obj.CreateTime(),
		HashInfoStr:	obj.GetHash().String(),
		HashInfo:	obj.GetHash().Export(),
		Sign:		common.Sign(obj, parentPath, isEncrypt(meta, reqPath)),
		Type:		utils.GetObjType(obj.GetName(), obj.IsDir()),
		Thumb:		thumb,
		MountDetails:	mountDetails,
	},
	RawURL:		rawURL,
	Readme:		getReadme(meta, reqPath),
	Header:		getHeader(meta, reqPath),
	Provider:	provider,
	Related:	toObjsResp(related, parentPath, isEncrypt(parentMeta, parentPath)),
})
```

本地后续调用：`server/handles.filterRelated`, `server/handles.getHeader`, `server/handles.getReadme`, `server/handles.isEncrypt`, `server/handles.toObjsResp`。


### `server/handles.FsGetDirectUploadInfo`

源码：`server/handles/direct_upload.go:29`。

请求绑定：`ShouldBind` → `server/handles.FsGetDirectUploadInfoReq`。

直接参数：header `Overwrite`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "file exists", 403)
common.SuccessResp(c, directUploadInfo)
```

本地后续调用：`server/handles.checkRelativePath`。


### `server/handles.FsGetSplit`

源码：`server/handles/fsread.go:265`。

请求绑定：`ShouldBind` → `server/handles.FsGetReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
```

本地后续调用：`server/handles.FsGet`, `server/handles.SharingGet`。


### `server/handles.FsList`

源码：`server/handles/fsread.go:80`。

```go
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.ErrorStrResp(c, "Refresh without permission", 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, FsListResp{
	Content:		toObjsResp(objs, reqPath, isEncrypt(meta, reqPath)),
	Total:			int64(total),
	Readme:			getReadme(meta, reqPath),
	Header:			getHeader(meta, reqPath),
	Write:			common.CanWrite(user, meta, reqPath),
	WriteContentBypass:	common.CanWriteContentBypassUserPerms(meta, reqPath),
	Provider:		provider,
	DirectUploadTools:	directUploadTools,
})
```

本地后续调用：`server/handles.getHeader`, `server/handles.getReadme`, `server/handles.isEncrypt`, `server/handles.pagination`, `server/handles.toObjsResp`。


### `server/handles.FsListSplit`

源码：`server/handles/fsread.go:60`。

请求绑定：`ShouldBind` → `server/handles.ListReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
```

本地后续调用：`server/handles.FsList`, `server/handles.SharingList`。


### `server/handles.FsMkdir`

源码：`server/handles/fsmanage.go:26`。

请求绑定：`ShouldBind` → `server/handles.MkdirOrLinkReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.FsMove`

源码：`server/handles/fsmanage.go:70`。

请求绑定：`ShouldBind` → `server/handles.MoveCopyReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Empty file names", 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, fmt.Sprintf("invalid file name [%s]", name), 400)
common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", name), 403)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, followErr, 500)
common.SuccessResp(c, gin.H{
	"message":	fmt.Sprintf("Successfully created %d move task(s)", len(addedTasks)),
	"tasks":	getTaskInfos(addedTasks),
})
common.SuccessResp(c, gin.H{
	"message": "Move operations completed immediately",
})
```

本地后续调用：`server/handles.checkRelativePath`, `server/handles.followSeedTransfer`, `server/handles.getTaskInfos`。


### `server/handles.FsOther`

源码：`server/handles/fsread.go:403`。

请求绑定：`ShouldBind` → `server/handles.FsOtherReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "password is incorrect or you have no permission", 403)
common.SuccessResp(c, res)
```


### `server/handles.FsRecursiveMove`

源码：`server/handles/fsbatch.go:26`。

请求绑定：`ShouldBind` → `server/handles.RecursiveMoveReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", movingFile.GetName()), 403)
common.SuccessWithMsgResp(c, fmt.Sprintf("Successfully moved %d %s", count, common.Pluralize(count, "file", "files")))
```


### `server/handles.FsRegexRename`

源码：`server/handles/fsbatch.go:217`。

请求绑定：`ShouldBind` → `server/handles.RegexRenameReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.checkRelativePath`。


### `server/handles.FsRemove`

源码：`server/handles/fsmanage.go:376`。

请求绑定：`ShouldBind` → `server/handles.RemoveReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Empty file names", 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.checkRelativePath`, `server/handles.followSeedRemove`。


### `server/handles.FsRemoveEmptyDirectory`

源码：`server/handles/fsmanage.go:511`。

请求绑定：`ShouldBind` → `server/handles.RemoveEmptyDirectoryReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.FsRename`

源码：`server/handles/fsmanage.go:311`。

请求绑定：`ShouldBind` → `server/handles.RenameReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, fmt.Sprintf("file [%s] exists", req.Name), 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.checkRelativePath`, `server/handles.followSeedRename`。


### `server/handles.FsStream`

源码：`server/handles/fsup.go:47`。

直接参数：header `File-Path`; header `As-Task`; header `Overwrite`; header `X-File-Size`; header `X-File-Md5`; header `X-File-Sha1`; header `X-File-Sha256`; header `Content-Type`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorStrResp(c, "file exists", 403)
common.ErrorStrResp(c, errs.IgnoredSystemFile.Error(), 403)
common.ErrorStrResp(c, err.Error(), 400)
common.ErrorStrResp(c, "seed sidecar generation requires synchronous upload", 400)
common.ErrorStrResp(c, "CAS sidecar generation requires a known file size: send Content-Length or X-File-Size", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
common.SuccessResp(c, gin.H{
	"task": getTaskInfo(t),
})
```

本地后续调用：`server/handles.getLastModified`, `server/handles.getTaskInfo`, `server/handles.seedSidecarWantsCAS`, `server/handles.shouldGenerateUploadSeed`, `server/handles.shouldIgnoreSystemFile`, `server/handles.uploadSeedSliceSize`, `server/handles.validateUploadSeedRequest`, `server/handles.writeUploadSeedSidecar`。


### `server/handles.Generate2FA`

源码：`server/handles/auth.go:149`。

```go
common.ErrorStrResp(c, model.GuestCannotGenerate2FA, 403)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{
	"qr":		"data:image/png;base64," + b64,
	"secret":	key.Secret(),
})
```


### `server/handles.GenerateMediaStrm`

源码：`server/handles/media_extra.go:143`。

请求绑定：`ShouldBindJSON` → `server/handles.GenerateMediaStrm.req`。

```go
common.ErrorStrResp(c, "未配置 STRM 输出目录", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{"written": n, "path": outPath})
```


### `server/handles.GenerateSeedForPaths`

源码：`server/handles/torrent.go:1337`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedGenerateReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "invalid seed file count", 400)
common.ErrorStrResp(c, "asynchronous seed generation requires an output_path", 400)
common.ErrorResp(c, addErr, 500)
common.SuccessResp(c, gin.H{"task": getTaskInfo(taskInfo), "async": true})
common.ErrorResp(c, genErr, status)
common.SuccessResp(c, gin.H{"artifacts": artifacts, "seed": seed})
```

本地后续调用：`server/handles.getTaskInfo`, `server/handles.toSeedGenerateParams`。


### `server/handles.GenerateTorrentForPath`

源码：`server/handles/torrent.go:641`。

请求绑定：`ShouldBind` → `server/handles.GenerateTorrentReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, fmt.Errorf("driver %s does not support the CAS rapid-upload extension", storage.GetStorage().Driver), 400)
common.ErrorResp(c, fmt.Errorf("failed to get the file: %w", err), 500)
common.ErrorResp(c, fmt.Errorf("generating a torrent for a folder is not supported"), 400)
common.ErrorResp(c, fmt.Errorf("file is too large to generate a torrent (max 1GB)"), 400)
common.ErrorResp(c, fmt.Errorf("failed to get the file link: %w", err), 500)
common.ErrorResp(c, fmt.Errorf("storage cannot stream, torrent generation failed: %w", err), 400)
common.ErrorResp(c, fmt.Errorf("failed to read the file: %w", err), 500)
common.ErrorResp(c, fmt.Errorf("failed to generate torrent: %w", err), 500)
common.SuccessResp(c, gin.H{
	"torrent_data":	base64.StdEncoding.EncodeToString(torrentData),
	"info_hash":	infoHash,
	"file_name":	obj.GetName() + ".torrent",
	"size":		len(torrentData),
	"with_cas":	req.WithCAS,
})
```


### `server/handles.GetAuthnCredentials`

源码：`server/handles/webauthn.go:228`。

```go
common.SuccessResp(c, res)
```


### `server/handles.GetDriverInfo`

源码：`server/handles/driver.go:19`。

直接参数：query `driver`。

```go
common.ErrorStrResp(c, fmt.Sprintf("driver [%s] not found", driverName), 404)
common.SuccessResp(c, items)
```


### `server/handles.GetManualScanProgress`

源码：`server/handles/scan.go:41`。

```go
common.SuccessResp(c, ret)
```


### `server/handles.GetMediaProgress`

源码：`server/handles/media_user.go:52`。

直接参数：query `item_id`; query `episode_index`。

```go
common.SuccessResp(c, nil)
common.SuccessResp(c, data)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.atoiDefault`, `server/handles.mediaUser`。


### `server/handles.GetMediaScanProgress`

源码：`server/handles/media.go:581`。

直接参数：query `media_type`。

```go
common.ErrorStrResp(c, "media_type 不能为空", 400)
common.SuccessResp(c, progress)
```


### `server/handles.GetMediaScrapeStats`

源码：`server/handles/media.go:605`。

```go
common.SuccessResp(c, MediaScrapeStatsResp{
	TMDB: scraper.GetTMDBStats(),
})
```


### `server/handles.GetMediaStreamURL`

源码：`server/handles/media_extra.go:169`。

直接参数：query `path`; query `id`; query `episode`，默认表达式 `"0"`。

```go
common.ErrorStrResp(c, "资源不存在", 404)
common.SuccessResp(c, gin.H{
	"url":		PlaybackStreamURL(c, rawPath),
	"path":		rawPath,
	"name":		stdpath.Base(rawPath),
	"proxied":	PlaybackProxyURL(c, rawPath),
})
common.ErrorStrResp(c, "无效的ID", 400)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.CanUserAccessPath`, `server/handles.MediaEpisodeFilePath`, `server/handles.MediaItemFilePath`, `server/handles.PlaybackProxyURL`, `server/handles.PlaybackStreamURL`, `server/handles.mediaUser`。


### `server/handles.GetMediaStrm`

源码：`server/handles/media_extra.go:223`。

直接参数：query `id`; query `episode`，默认表达式 `"0"`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorStrResp(c, "资源不存在", 404)
c.Header("Content-Type", "text/plain; charset=utf-8")
c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(filename)))
c.String(200, content)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.CanUserAccessPath`, `server/handles.MediaEpisodeFilePath`, `server/handles.MediaItemFilePath`, `server/handles.mediaUser`。


### `server/handles.GetMediaUserDataBatch`

源码：`server/handles/media_user.go:18`。

直接参数：query `ids`。

```go
common.SuccessResp(c, []model.MediaUserData{})
common.ErrorResp(c, err, 500)
common.SuccessResp(c, list)
```

本地后续调用：`server/handles.mediaUser`。


### `server/handles.GetMeta`

源码：`server/handles/meta.go:96`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, meta)
```


### `server/handles.GetOIDCClient`

源码：`server/handles/ssologin.go:253`。

本地后续调用：`server/handles.ssoRedirectUri`。


### `server/handles.GetProgress`

源码：`server/handles/index.go:123`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, progress)
```


### `server/handles.GetSetting`

源码：`server/handles/setting.go:30`。

直接参数：query `key`; query `keys`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, item)
common.SuccessResp(c, items)
```


### `server/handles.GetSharing`

源码：`server/handles/sharing.go:357`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "sharing not found", 404)
common.SuccessResp(c, SharingResp{
	Sharing:	s,
	CreatorName:	s.Creator.Username,
	CreatorRole:	s.Creator.Role,
})
```


### `server/handles.GetStorage`

源码：`server/handles/storage.go:168`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, storage)
```


### `server/handles.GetUser`

源码：`server/handles/user.go:102`。

直接参数：query `id`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, user)
```


### `server/handles.ImportMediaDB`

源码：`server/handles/media.go:1310`。

直接参数：query `scan_path_id`。

```go
common.ErrorResp(c, err, status)
common.ErrorStrResp(c, "不支持的导入文件版本", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{
	"scan_paths_created":	spCreated,
	"scan_paths_updated":	spUpdated,
	"items_created":	itemCreated,
	"items_updated":	itemUpdated,
})
```

本地后续调用：`server/handles.cleanupMediaImportedCovers`, `server/handles.readMediaImportRequest`。


### `server/handles.InitSetup`

源码：`server/handles/setup.go:36`。

请求绑定：`ShouldBind` → `server/handles.InitSetupReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "system has already been initialized", 400)
common.ErrorResp(c, err, 500, true)
common.ErrorStrResp(c, "password must be at least 4 characters", 400)
common.SuccessResp(c)
```


### `server/handles.InitStatus`

源码：`server/handles/setup.go:15`。

```go
common.SuccessResp(c, gin.H{"initialized": false})
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, gin.H{"initialized": true})
```


### `server/handles.Link`

源码：`server/handles/fsmanage.go:609`。

请求绑定：`ShouldBind` → `server/handles.MkdirOrLinkReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, model.Link{
	URL: fmt.Sprintf("%s/p%s?d&sign=%s",
		common.GetApiUrl(c),
		utils.EncodePath(rawPath, true),
		sign.Sign(rawPath)),
})
common.SuccessResp(c, link)
```


### `server/handles.ListDriverInfo`

源码：`server/handles/driver.go:11`。

```go
common.SuccessResp(c, op.GetDriverInfoMap())
```


### `server/handles.ListDriverNames`

源码：`server/handles/driver.go:15`。

```go
common.SuccessResp(c, op.GetDriverNames())
```


### `server/handles.ListMediaAlbumItemsAdmin`

源码：`server/handles/media_album.go:48`。

直接参数：query `scan_path_id`; query `album_name`; query `album_artist`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, items)
```


### `server/handles.ListMediaAlbumsAdmin`

源码：`server/handles/media_album.go:19`。

直接参数：query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"20"`; query `scan_path_id`; query `keyword`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: albums, Total: total})
```

本地后续调用：`server/handles.clampMediaPage`。


### `server/handles.ListMediaAnnotationsAPI`

源码：`server/handles/media_reader.go:47`。

直接参数：query `item_id`。

```go
common.SuccessResp(c, []model.MediaAnnotation{})
common.ErrorResp(c, err, 500)
common.SuccessResp(c, list)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.atoiDefault`, `server/handles.mediaUser`。


### `server/handles.ListMediaAppKeys`

源码：`server/handles/media_extra.go:64`。

```go
common.ErrorStrResp(c, "未登录", 401)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, keys)
```

本地后续调用：`server/handles.mediaUser`。


### `server/handles.ListMediaConfigs`

源码：`server/handles/media.go:38`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, cfgs)
```


### `server/handles.ListMediaFavoritesAPI`

源码：`server/handles/media_user.go:172`。

直接参数：query `media_type`; query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"20"`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: items[start:end], Total: int64(total)})
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.atoiDefault`, `server/handles.mediaUser`。


### `server/handles.ListMediaItemsAdmin`

源码：`server/handles/media.go:241`。

直接参数：query `media_type`; query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"20"`; query `keyword`; query `order_by`，默认表达式 `"name"`; query `order_dir`，默认表达式 `"asc"`; query `scan_path_id`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: items, Total: total})
```

本地后续调用：`server/handles.clampMediaPage`。


### `server/handles.ListMediaResumeAPI`

源码：`server/handles/media_user.go:221`。

直接参数：query `media_type`; query `limit`，默认表达式 `"20"`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, items)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.atoiDefault`, `server/handles.mediaUser`。


### `server/handles.ListMediaScanPaths`

源码：`server/handles/media.go:91`。

直接参数：query `media_type`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, paths)
```


### `server/handles.ListMetas`

源码：`server/handles/meta.go:16`。

请求绑定：`ShouldBind` → `internal/model.PageReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, common.PageResp{
	Content:	metas,
	Total:		total,
})
```


### `server/handles.ListMyPublicKey`

源码：`server/handles/sshkey.go:50`。

```go
common.ErrorStrResp(c, "user invalid", 401)
```

本地后续调用：`server/handles.list`。


### `server/handles.ListPublicKeys`

源码：`server/handles/sshkey.go:83`。

直接参数：query `uid`。

```go
common.ErrorStrResp(c, "user id format invalid", 400)
common.ErrorStrResp(c, "user invalid", 404)
```

本地后续调用：`server/handles.list`。


### `server/handles.ListSettings`

源码：`server/handles/setting.go:64`。

直接参数：query `group`; query `groups`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, settings)
```


### `server/handles.ListSharings`

源码：`server/handles/sharing.go:372`。

请求绑定：`ShouldBind` → `internal/model.PageReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, common.PageResp{
	Content: utils.MustSliceConvert(sharings, func(s model.Sharing) SharingResp {
		return SharingResp{
			Sharing:	&s,
			CreatorName:	s.Creator.Username,
			CreatorRole:	s.Creator.Role,
		}
	}),
	Total:	total,
})
```


### `server/handles.ListStorages`

源码：`server/handles/storage.go:77`。

请求绑定：`ShouldBind` → `internal/model.PageReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{
	Content:	makeStorageResp(c, storages),
	Total:		total,
})
```

本地后续调用：`server/handles.makeStorageResp`。


### `server/handles.ListTranscodeJobs`

源码：`server/handles/transcode.go:614`。

```go
common.SuccessResp(c, transcode.Default().Scheduler.ListJobs())
```


### `server/handles.ListTranscodeWorkers`

源码：`server/handles/transcode.go:609`。

```go
common.SuccessResp(c, transcode.Default().Registry.List())
```


### `server/handles.ListUsers`

源码：`server/handles/user.go:13`。

请求绑定：`ShouldBind` → `internal/model.PageReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, common.PageResp{
	Content:	users,
	Total:		total,
})
```


### `server/handles.LoadAllStorages`

源码：`server/handles/storage.go:183`。

```go
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.LogOut`

源码：`server/handles/auth.go:206`。

直接参数：header `Authorization`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.Login`

源码：`server/handles/auth.go:27`。

请求绑定：`ShouldBind` → `server/handles.LoginReq`。

```go
common.ErrorResp(c, err, 400)
```

本地后续调用：`server/handles.loginHash`。


### `server/handles.LoginHash`

源码：`server/handles/auth.go:38`。

请求绑定：`ShouldBind` → `server/handles.LoginReq`。

```go
common.ErrorResp(c, err, 400)
```

本地后续调用：`server/handles.loginHash`。


### `server/handles.LoginLdap`

源码：`server/handles/ldap_login.go:13`。

请求绑定：`ShouldBind` → `server/handles.LoginReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "ldap is not enabled", 403)
common.ErrorStrResp(c, "login via ldap is not allowed", 403)
common.ErrorStrResp(c, "Too many unsuccessful sign-in attempts have been made using an incorrect username or password, Try again later.", 429)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, err, 400, true)
common.SuccessResp(c, gin.H{"token": token})
```


### `server/handles.MediaEmbyEnabled`

源码：`server/handles/media_gate.go:30`。

本地后续调用：`server/handles.MediaEnabled`。


### `server/handles.MediaEnabled`

源码：`server/handles/media_gate.go:25`。


### `server/handles.MediaEpisodeFilePath`

源码：`server/handles/media_gate.go:123`。

本地后续调用：`server/handles.MediaItemFilePath`。


### `server/handles.MediaItemFilePath`

源码：`server/handles/media_gate.go:111`。


### `server/handles.MediaSubsonicEnabled`

源码：`server/handles/media_gate.go:35`。

本地后续调用：`server/handles.MediaEnabled`。


### `server/handles.MediaTypeEnabled`

源码：`server/handles/media_gate.go:142`。


### `server/handles.MediaTypeEnabledList`

源码：`server/handles/media_gate.go:151`。

本地后续调用：`server/handles.MediaTypeEnabled`。


### `server/handles.MultipartAbort`

源码：`server/handles/multipart.go:210`。

直接参数：header `X-Upload-Id`。

```go
common.ErrorResp(c, err, multipartErrCode(err))
common.SuccessResp(c)
```

本地后续调用：`server/handles.multipartErrCode`。


### `server/handles.MultipartChunk`

源码：`server/handles/multipart.go:138`。

直接参数：header `X-Upload-Id`; header `X-Chunk-Index`。

```go
common.ErrorStrResp(c, "invalid X-Chunk-Index header", 400)
common.ErrorWithDataResp(c, err, multipartErrCode(err), snap)
common.SuccessResp(c, snap)
```

本地后续调用：`server/handles.multipartChunkSize`, `server/handles.multipartErrCode`。


### `server/handles.MultipartComplete`

源码：`server/handles/multipart.go:167`。

直接参数：header `X-Upload-Id`。

```go
common.ErrorWithDataResp(c, err, multipartErrCode(err), snap)
common.SuccessResp(c, snap)
```

本地后续调用：`server/handles.multipartErrCode`。


### `server/handles.MultipartInit`

源码：`server/handles/multipart.go:48`。

直接参数：header `File-Path`; header `X-File-Size`; header `X-Chunk-Size`; header `Overwrite`; header `X-File-Md5`; header `X-File-Sha1`; header `X-File-Sha256`; header `Content-Type`。

```go
common.ErrorStrResp(c, "multipart upload is disabled", 403)
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorStrResp(c, "multipart upload requires a valid X-File-Size header", 400)
common.ErrorStrResp(c, "multipart upload requires a positive X-File-Size; upload empty files via /fs/put", 400)
common.ErrorStrResp(c, "file exists", 403)
common.ErrorStrResp(c, errs.IgnoredSystemFile.Error(), 403)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, errs.UploadNotSupported, 405)
common.SuccessResp(c, MultipartInitResp{SessionSnapshot: snap, Resumed: resumed})
```

本地后续调用：`server/handles.getLastModified`, `server/handles.multipartChunkSize`, `server/handles.shouldIgnoreSystemFile`。


### `server/handles.MultipartStatus`

源码：`server/handles/multipart.go:180`。

直接参数：query `upload_id`; query `path`; query `size`。

```go
common.ErrorResp(c, err, multipartErrCode(err))
common.SuccessResp(c, snap)
common.ErrorResp(c, err, 403)
common.ErrorStrResp(c, "status lookup requires upload_id, or path and size", 400)
```

本地后续调用：`server/handles.multipartErrCode`。


### `server/handles.OIDCLoginCallback`

源码：`server/handles/ssologin.go:323`。

直接参数：query `method`; query `state`; query `code`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "incorrect or expired state parameter", 400)
common.ErrorStrResp(c, "no id_token found in oauth2 token", 400)
common.ErrorStrResp(c, "cannot get username from OIDC provider", 400)
common.ErrorResp(c, err, 500)
c.Redirect(302, common.GetApiUrl(c)+"/@manage?sso_id="+bindingProof)
c.Redirect(302, common.GetApiUrl(c)+"/@login?token="+token)
```

本地后续调用：`server/handles.GetOIDCClient`, `server/handles.autoRegister`, `server/handles.generateSSOBindingToken`, `server/handles.parseJWT`, `server/handles.parseSSOBindingToken`, `server/handles.ssoPostMessage`, `server/handles.verifyState`。


### `server/handles.OfflineDownloadTools`

源码：`server/handles/offline_download.go:319`。

直接参数：query `path`。

```go
common.SuccessResp(c, tools)
```


### `server/handles.ParseSeed`

源码：`server/handles/torrent.go:891`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedDataReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, gin.H{
	"format":	format,
	"seed":		seed,
	"files":	seed.Files,
	"total_size":	seedTotalSize(seed),
	"diagnostics":	seedDiagnostics(seed),
	"conversions":	seedConversionStates(seed, format),
	"capabilities": gin.H{
		"rapid_upload":	hasRapidHashes, "offline_download": hasSource || format == "torrent",
		"transfer":	hasSource, "convert": true, "edit": true, "recalculate": true,
	},
	"direct_preview":	len(seed.Files) == 1 && setting.GetBool(conf.SeedSingleDirectPreview),
})
```

本地后续调用：`server/handles.decodeSeedData`, `server/handles.firstUsableSeedSource`, `server/handles.seedConversionStates`, `server/handles.seedDiagnostics`, `server/handles.seedTotalSize`。


### `server/handles.ParseTorrent`

源码：`server/handles/torrent.go:369`。

请求绑定：`ShouldBind` → `server/handles.ParseTorrentReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, fmt.Errorf("torrent data is too large (max 10MB)"), 400)
common.ErrorResp(c, fmt.Errorf("invalid base64 data: %w", err), 400)
common.ErrorResp(c, fmt.Errorf("failed to parse torrent: %w", err), 400)
common.SuccessResp(c, resp)
```

本地后续调用：`server/handles.validateParsedTorrent`。


### `server/handles.PlaybackProxyURL`

源码：`server/handles/media_gate.go:231`。

本地后续调用：`server/handles.SignedProxyURLWithBase`。


### `server/handles.PlaybackStreamURL`

源码：`server/handles/media_gate.go:226`。

本地后续调用：`server/handles.SignedStreamURLWithBase`。


### `server/handles.PublicGetAlbum`

源码：`server/handles/media.go:1037`。

直接参数：query `album_name`; query `album_artist`。

```go
common.ErrorStrResp(c, "album_name 和 album_artist 不能同时为空", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, filtered)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.mediaUser`。


### `server/handles.PublicGetMedia`

源码：`server/handles/media.go:943`。

直接参数：path `id`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 404)
common.ErrorStrResp(c, "资源不存在", 404)
common.SuccessResp(c, item)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.mediaUser`。


### `server/handles.PublicGetMediaExif`

源码：`server/handles/media_exif.go:73`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "无效的ID", 400)
common.ErrorResp(c, err, 404)
common.ErrorStrResp(c, "资源不存在", 404)
common.ErrorStrResp(c, "只有图片文件支持 EXIF 信息", 400)
common.SuccessResp(c, loadImageExif(c.Request.Context(), user, item))
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.loadImageExif`, `server/handles.mediaUser`。


### `server/handles.PublicListAlbums`

源码：`server/handles/media.go:963`。

直接参数：query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"40"`; query `keyword`; query `scan_path_id`; query `album_artist`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: albums, Total: total})
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.clampMediaPage`, `server/handles.mediaUser`, `server/handles.userPathPrefix`。


### `server/handles.PublicListArtists`

源码：`server/handles/media.go:1001`。

直接参数：query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"40"`; query `keyword`; query `scan_path_id`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: artists, Total: total})
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.clampMediaPage`, `server/handles.mediaUser`, `server/handles.userPathPrefix`。


### `server/handles.PublicListFolders`

源码：`server/handles/media.go:1060`。

直接参数：query `media_type`。

```go
common.ErrorStrResp(c, "media_type 不能为空", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, filtered)
```

本地后续调用：`server/handles.CanUserAccessPath`, `server/handles.mediaUser`。


### `server/handles.PublicListMedia`

源码：`server/handles/media.go:892`。

直接参数：query `media_type`; query `page`，默认表达式 `"1"`; query `page_size`，默认表达式 `"40"`; query `order_by`，默认表达式 `"name"`; query `order_dir`，默认表达式 `"asc"`; query `folder_path`; query `keyword`; query `type_tag`; query `content_tag`; query `scan_path_id`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{Content: filtered, Total: total})
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.clampMediaPage`, `server/handles.mediaUser`, `server/handles.userPathPrefix`。


### `server/handles.PublicListScanPaths`

源码：`server/handles/media.go:1082`。

直接参数：query `media_type`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, filtered)
```

本地后续调用：`server/handles.CanUserAccessPath`, `server/handles.mediaUser`。


### `server/handles.PublicMediaProxy`

源码：`server/handles/media_image.go:66`。

直接参数：query `url`。

```go
c.Status(http.StatusNotFound)
c.Status(http.StatusBadRequest)
c.Status(http.StatusForbidden)
c.Status(http.StatusBadGateway)
c.Status(http.StatusUnsupportedMediaType)
c.Header("Cache-Control", "public, max-age=604800")
c.Data(http.StatusOK, ct, data)
```

本地后续调用：`server/handles.MediaEnabled`, `server/handles.fetchCover`, `server/handles.safeCoverURL`, `server/handles.setSafeImageHeaders`。


### `server/handles.PublicMediaStatus`

源码：`server/handles/media_extra.go:37`。

```go
common.SuccessResp(c, status)
```

本地后续调用：`server/handles.MediaEmbyEnabled`, `server/handles.MediaEnabled`, `server/handles.MediaSubsonicEnabled`, `server/handles.MediaTypeEnabledList`。


### `server/handles.PublicMediaThumb`

源码：`server/handles/media_image.go:155`。

直接参数：query `path`。

```go
c.Status(http.StatusNotFound)
c.Status(http.StatusBadRequest)
c.Status(http.StatusForbidden)
c.Status(http.StatusUnsupportedMediaType)
c.Header("Content-Type", ct)
c.Header("Cache-Control", "public, max-age=604800")
c.File(full)
```

本地后续调用：`server/handles.MediaEnabled`, `server/handles.setSafeImageHeaders`, `server/handles.thumbnailRoot`。


### `server/handles.PublicSettings`

源码：`server/handles/setting.go:145`。

```go
common.SuccessResp(c, op.GetPublicSettingsMap())
```


### `server/handles.QuickSaveSeed`

源码：`server/handles/torrent.go:2208`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedQuickSaveReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, seedSaveErrorCode(err))
common.SuccessResp(c, resp)
```

本地后续调用：`server/handles.applyChannelUpdate`, `server/handles.decodeSeedData`, `server/handles.resolveSeedTransitPath`, `server/handles.saveSeedFilesToPath`, `server/handles.seedSaveErrorCode`, `server/handles.seedSaveResultsError`, `server/handles.transferSeedViaTransit`。


### `server/handles.ResetToken`

源码：`server/handles/setting.go:19`。

```go
common.ErrorResp(c, err, 500)
common.SuccessResp(c, token)
```


### `server/handles.SSOLoginCallback`

源码：`server/handles/ssologin.go:414`。

直接参数：query `method`; query `state`。

```go
common.ErrorResp(c, errors.New("sso login is disabled"), 500)
common.ErrorResp(c, errors.New("invalid request"), 500)
common.ErrorStrResp(c, "invalid platform", 400)
common.ErrorStrResp(c, "No code provided", 400)
common.ErrorStrResp(c, "incorrect or expired state parameter", 400)
common.ErrorResp(c, err, 400)
common.ErrorResp(c, errors.New("error occurred"), 400)
common.ErrorResp(c, err, 500)
c.Redirect(302, common.GetApiUrl(c)+"/@manage?sso_id="+bindingProof)
c.Redirect(302, common.GetApiUrl(c)+"/@login?token="+token)
```

本地后续调用：`server/handles.OIDCLoginCallback`, `server/handles.autoRegister`, `server/handles.generateSSOBindingToken`, `server/handles.parseSSOBindingToken`, `server/handles.ssoPostMessage`。


### `server/handles.SSOLoginRedirect`

源码：`server/handles/ssologin.go:180`。

直接参数：query `method`。

```go
common.ErrorStrResp(c, "Single sign-on is not enabled", 403)
common.ErrorStrResp(c, "no method provided", 400)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, err.Error(), 400)
c.Redirect(http.StatusFound, oauth2Config.AuthCodeURL(bindingState))
common.ErrorStrResp(c, "invalid platform", 400)
c.Redirect(302, rUrl+urlValues.Encode())
```

本地后续调用：`server/handles.GetOIDCClient`, `server/handles.generateSSOBindingToken`, `server/handles.generateState`, `server/handles.ssoRedirectUri`。


### `server/handles.SaveMediaConfig`

源码：`server/handles/media.go:65`。

请求绑定：`ShouldBindJSON` → `server/handles.SaveMediaConfigReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.SaveMediaProgress`

源码：`server/handles/media_user.go:82`。

请求绑定：`ShouldBindJSON` → `server/handles.saveProgressReq`。

```go
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorStrResp(c, "invalid request", 400)
common.ErrorStrResp(c, "not found", 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, data)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.mediaUser`, `server/handles.minFloat`。


### `server/handles.SaveSettings`

源码：`server/handles/setting.go:50`。

请求绑定：`ShouldBind` → `[]internal/model.SettingItem`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.Search`

源码：`server/handles/search.go:27`。

请求绑定：`ShouldBind` → `server/handles.SearchReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, common.PageResp{
	Content:	utils.MustSliceConvert(nodes, nodeToSearchResp),
	Total:		total,
})
```

本地后续调用：`server/handles.isSearchNodeAccessible`。


### `server/handles.SeedCapabilities`

源码：`server/handles/torrent.go:1416`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedCapabilityReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "invalid seed file count", 400)
common.ErrorResp(c, expandErr, 400)
common.ErrorStrResp(c, "no files found under the selected paths", 400)
common.ErrorResp(c, metaErr, 500, true)
common.ErrorResp(c, getErr, 400)
common.ErrorResp(c, fmt.Errorf("seed path must be a readable file: %s", requestedPath), 400)
common.SuccessResp(c, gin.H{
	"formats":	gin.H{"oss": true, "torrent": true, "cas": true},
	"files":	files, "existing_hashes": existingHashes, "estimated_traffic": estimatedTraffic,
	"default_matrix":	loadSeedDefaultMatrix(), "trackers": loadSeedDefaultTrackers(),
})
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorStrResp(c, "policy must be off, on, or inherit", 400)
common.SuccessResp(c, gin.H{
	"driver":	storage.Config().Name, "global_policy": globalPolicy,
	"resolved_policy":	policy, "files": files, "driver_supports": driverSupports,
})
```

本地后续调用：`server/handles.decodeSeedData`, `server/handles.directSourceAvailable`, `server/handles.firstUsableSeedSource`, `server/handles.loadSeedDefaultMatrix`, `server/handles.loadSeedDefaultTrackers`, `server/handles.seedFileSupportsRapidUpload`, `server/handles.seedRapidUploaderFor`。


### `server/handles.Set115`

源码：`server/handles/offline_download.go:123`。

请求绑定：`ShouldBind` → `server/handles.Set115Req`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.Set115Open`

源码：`server/handles/offline_download.go:145`。

请求绑定：`ShouldBind` → `server/handles.Set115OpenReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.Set123Open`

源码：`server/handles/offline_download.go:190`。

请求绑定：`ShouldBind` → `server/handles.Set123OpenReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.Set123Pan`

源码：`server/handles/offline_download.go:167`。

请求绑定：`ShouldBind` → `server/handles.Set123PanReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetAria2`

源码：`server/handles/offline_download.go:60`。

请求绑定：`ShouldBind` → `server/handles.SetAria2Req`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, version)
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`。


### `server/handles.SetEnableSharing`

源码：`server/handles/sharing.go:594`。

直接参数：query `id`。

```go
common.ErrorStrResp(c, "sharing not found", 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.SetGuangYaPan`

源码：`server/handles/offline_download.go:301`。

请求绑定：`ShouldBind` → `server/handles.SetGuangYaPanReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetMediaFavorite`

源码：`server/handles/media_user.go:138`。

请求绑定：`ShouldBindJSON` → `server/handles.favoriteReq`。

```go
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorStrResp(c, "invalid request", 400)
common.ErrorStrResp(c, "not found", 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{"favorite": data.IsFavorite})
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.mediaUser`。


### `server/handles.SetPikPak`

源码：`server/handles/offline_download.go:213`。

请求绑定：`ShouldBind` → `server/handles.SetPikPakReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetQbittorrent`

源码：`server/handles/offline_download.go:82`。

请求绑定：`ShouldBind` → `server/handles.SetQbittorrentReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`。


### `server/handles.SetThunder`

源码：`server/handles/offline_download.go:235`。

请求绑定：`ShouldBind` → `server/handles.SetThunderReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetThunderBrowser`

源码：`server/handles/offline_download.go:279`。

请求绑定：`ShouldBind` → `server/handles.SetThunderBrowserReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetThunderX`

源码：`server/handles/offline_download.go:257`。

请求绑定：`ShouldBind` → `server/handles.SetThunderXReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`, `server/handles.validateOfflineDownloadStorage`。


### `server/handles.SetTransmission`

源码：`server/handles/offline_download.go:103`。

请求绑定：`ShouldBind` → `server/handles.SetTransmissionReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c, "ok")
```

本地后续调用：`server/handles.saveAndInitOfflineDownloadTool`。


### `server/handles.SharingArchiveList`

源码：`server/handles/sharing.go:150`。

直接参数：query `type`。

```go
common.ErrorStrResp(c, "sharing archives previewing is not allowed", 403)
common.ErrorStrResp(c, "invalid share id", 400)
common.SuccessResp(c, common.PageResp{
	Content:	ret,
	Total:		int64(total),
})
```

本地后续调用：`server/handles.dealError`, `server/handles.pagination`, `server/handles.toObjsRespWithoutSignAndThumb`。


### `server/handles.SharingArchiveMeta`

源码：`server/handles/sharing.go:107`。

直接参数：query `type`。

```go
common.ErrorStrResp(c, "sharing archives previewing is not allowed", 403)
common.ErrorStrResp(c, "invalid share id", 400)
common.SuccessResp(c, ArchiveMetaResp{
	Comment:	ret.GetComment(),
	IsEncrypted:	ret.IsEncrypted(),
	Content:	toContentResp(ret.GetTree()),
	Sort:		ret.Sort,
	RawURL:		url,
	Sign:		"",
})
```

本地后续调用：`server/handles.dealError`, `server/handles.toContentResp`。


### `server/handles.SharingGet`

源码：`server/handles/sharing.go:24`。

```go
common.ErrorStrResp(c, "invalid share id", 400)
common.SuccessResp(c, FsGetResp{
	ObjResp: ObjResp{
		Name:		obj.GetName(),
		Size:		obj.GetSize(),
		IsDir:		obj.IsDir(),
		Modified:	obj.ModTime(),
		Created:	obj.CreateTime(),
		HashInfoStr:	obj.GetHash().String(),
		HashInfo:	obj.GetHash().Export(),
		Sign:		"",
		Type:		utils.GetObjType(obj.GetName(), obj.IsDir()),
		Thumb:		thumb,
	},
	RawURL:		url,
	Readme:		s.Readme,
	Header:		s.Header,
	Provider:	"unknown",
	Related:	nil,
})
```

本地后续调用：`server/handles.dealError`。


### `server/handles.SharingList`

源码：`server/handles/sharing.go:68`。

```go
common.ErrorStrResp(c, "invalid share id", 400)
common.SuccessResp(c, FsListResp{
	Content: utils.MustSliceConvert(objs, func(obj model.Obj) ObjResp {
		thumb, _ := model.GetThumb(obj)
		return ObjResp{
			Name:		obj.GetName(),
			Size:		obj.GetSize(),
			IsDir:		obj.IsDir(),
			Modified:	obj.ModTime(),
			Created:	obj.CreateTime(),
			HashInfoStr:	obj.GetHash().String(),
			HashInfo:	obj.GetHash().Export(),
			Sign:		"",
			Thumb:		thumb,
			Type:		utils.GetObjType(obj.GetName(), obj.IsDir()),
		}
	}),
	Total:		int64(total),
	Readme:		s.Readme,
	Header:		s.Header,
	Write:		false,
	Provider:	"unknown",
})
```

本地后续调用：`server/handles.dealError`, `server/handles.pagination`。


### `server/handles.SignedProxyURLWithBase`

源码：`server/handles/media_gate.go:208`。


### `server/handles.SignedStreamURLWithBase`

源码：`server/handles/media_gate.go:197`。


### `server/handles.StartManualScan`

源码：`server/handles/scan.go:14`。

请求绑定：`ShouldBind` → `server/handles.ManualScanReq`。

```go
common.ErrorResp(c, err, 400)
common.SuccessResp(c)
```


### `server/handles.StartMediaScan`

源码：`server/handles/media.go:549`。

请求绑定：`ShouldBindJSON` → `server/handles.ScanMediaReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "该媒体库未启用", 400)
common.ErrorResp(c, err, 404)
common.SuccessResp(c)
```


### `server/handles.StartMediaScrape`

源码：`server/handles/media.go:618`。

请求绑定：`ShouldBindJSON` → `server/handles.ScrapeMediaReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, e, 404)
common.SuccessResp(c)
```

本地后续调用：`server/handles.hasAnyScrapedField`, `server/handles.prepareMediaScrapeCandidate`。


### `server/handles.StopIndex`

源码：`server/handles/index.go:95`。

```go
common.ErrorStrResp(c, "index is not running", 400)
common.SuccessResp(c)
```


### `server/handles.StopManualScan`

源码：`server/handles/scan.go:27`。

```go
common.ErrorStrResp(c, "manual scan is not running", 400)
common.SuccessResp(c)
```


### `server/handles.SyncMediaAnnotationsAPI`

源码：`server/handles/media_reader.go:82`。

请求绑定：`ShouldBindJSON` → `server/handles.mediaAnnotationSyncReq`。

```go
common.ErrorStrResp(c, "invalid request", 400)
common.ErrorStrResp(c, "too many annotations", 400)
common.ErrorStrResp(c, "invalid annotation", 400)
common.ErrorStrResp(c, "annotation too large", 400)
common.ErrorStrResp(c, "annotations too large", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, gin.H{"saved": len(upserts), "deleted": len(deletes)})
```

本地后续调用：`server/handles.mediaAnnotationTarget`。


### `server/handles.TorrentRapidUpload`

源码：`server/handles/torrent.go:447`。

请求绑定：`ShouldBind` → `server/handles.TorrentRapidUploadReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorStrResp(c, "torrent data exceeds the maximum size", 413)
common.ErrorResp(c, fmt.Errorf("invalid base64 data: %w", err), 400)
common.ErrorResp(c, fmt.Errorf("failed to parse torrent: %w", err), 400)
common.ErrorResp(c, fmt.Errorf("torrent has no CAS metadata, rapid upload is not possible"), 400)
common.ErrorResp(c, err, 500)
common.ErrorResp(c, fmt.Errorf("failed to get the destination folder: %w", err), 500)
common.ErrorResp(c, errs.NotFolder, 400)
common.ErrorResp(c, fmt.Errorf("target driver %s does not support hash-based rapid upload (CAS)", storage.GetStorage().Driver), 400)
common.ErrorResp(c, fmt.Errorf("this seed cannot be used for rapid upload: only a single file whose size matches its hashes is supported"), 400)
common.ErrorResp(c, fmt.Errorf("rapid upload failed: %w", err), 400)
common.SuccessResp(c, gin.H{
	"message":	"rapid upload succeeded",
	"file_name":	obj.GetName(),
	"file_size":	obj.GetSize(),
})
```

本地后续调用：`server/handles.buildSeedRapidUploadRequest`。


### `server/handles.TranscodePlay`

源码：`server/handles/transcode.go:81`。

请求绑定：`ShouldBindJSON` → `server/handles.TranscodePlay.req`。

```go
common.SuccessResp(c, gin.H{"transcode": false, "reason": "disabled"})
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "Guest user is disabled, login please", 401)
common.ErrorStrResp(c, "path is not accessible", 403)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "path is a directory", 400)
common.SuccessResp(c, gin.H{
	"transcode":	false,
	"reason":	dec.Reason,
})
common.ErrorStrResp(
	c,
	"transcode needs site_url (or an HTTP port) to fetch the source",
	503,
)
common.ErrorStrResp(c, "unsupported transcode source", 400)
common.ErrorResp(c, err, 429)
common.ErrorStrResp(c, "too many concurrent transcode requests", 429)
```

本地后续调用：`server/handles.CanUserAccessPath`, `server/handles.encodePath`, `server/handles.mediaUser`, `server/handles.resolveLocalFilePath`, `server/handles.respondTranscodeJob`, `server/handles.trustedInternalBaseURL`。


### `server/handles.UpdateCurrent`

源码：`server/handles/auth.go:108`。

请求绑定：`ShouldBind` → `internal/model.User`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, model.GuestCannotUpdateProfile, 403)
common.ErrorStrResp(c, "invalid or expired SSO binding proof", 400)
common.ErrorStrResp(c, "SSO account is already bound to another user", 409)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.parseSSOBindingToken`。


### `server/handles.UpdateIndex`

源码：`server/handles/index.go:43`。

请求绑定：`ShouldBind` → `server/handles.UpdateIndexReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "index is running", 400)
common.ErrorStrResp(c, "update is not supported for current index", 400)
common.SuccessResp(c)
```


### `server/handles.UpdateMediaAlbumAdmin`

源码：`server/handles/media_album.go:89`。

请求绑定：`ShouldBindJSON` → `server/handles.UpdateMediaAlbumReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "缺少专辑定位字段", 400)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "专辑下没有曲目", 404)
common.ErrorStrResp(c, "封面数据过大（上限 12MB）", 400)
common.SuccessResp(c, gin.H{"affected": len(items)})
```

本地后续调用：`server/handles.removeUploadedCover`。


### `server/handles.UpdateMediaItemAdmin`

源码：`server/handles/media.go:325`。

请求绑定：`ShouldBindJSON` → `server/handles.UpdateMediaItemReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, err.Error(), 400)
common.ErrorStrResp(c, "选集必须是 [{file_name,index,title}] 形式的 JSON 数组", 400)
common.ErrorStrResp(c, fmt.Sprintf("第 %d 个选集缺少 file_name", i+1), 400)
common.ErrorStrResp(c, "video_type 只能是 movie 或 tv", 400)
common.ErrorStrResp(c, "封面数据过大（上限 12MB）", 400)
common.ErrorResp(c, err, 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.removeUploadedCover`, `server/handles.validateJSONArray`。


### `server/handles.UpdateMediaScanPath`

源码：`server/handles/media.go:169`。

请求绑定：`ShouldBindJSON` → `server/handles.UpdateMediaScanPathReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.clampMergeDepth`。


### `server/handles.UpdateMeta`

源码：`server/handles/meta.go:53`。

请求绑定：`ShouldBind` → `internal/model.Meta`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, fmt.Sprintf("%s is illegal: %s", r, err.Error()), 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```

本地后续调用：`server/handles.validHide`。


### `server/handles.UpdateSeed`

源码：`server/handles/torrent.go:1686`。

请求绑定：`ShouldBindJSON` → `server/handles.SeedUpdateReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, srcErr, 400)
common.ErrorStrResp(c, "remove_files requires at least one non-empty path", 400)
common.ErrorStrResp(c, "cannot remove all files from a seed", 400)
common.ErrorStrResp(c, "piece_size must be between 16384 and 67108864", 400)
common.ErrorStrResp(c, "recalculate requires at least one source_path", 400)
common.ErrorResp(c, rehashErr, 400)
common.ErrorResp(c, joinErr, 403)
common.ErrorResp(c, metaErr, 500, true)
common.ErrorResp(c, errs.PermissionDenied, 403)
common.ErrorResp(c, putErr, 500)
common.SuccessResp(c, result)
```

本地后续调用：`server/handles.applySeedMatrix`, `server/handles.applySeedUpdateOptions`, `server/handles.checkSeedShareValidity`, `server/handles.decodeSeedData`, `server/handles.normalizedSeedMatrix`, `server/handles.rehashSeedFile`, `server/handles.validateSeedSource`。


### `server/handles.UpdateSeedChannels`

源码：`server/handles/torrent.go:1579`。

本地后续调用：`server/handles.UpdateSeed`。


### `server/handles.UpdateSharing`

源码：`server/handles/sharing.go:432`。

请求绑定：`ShouldBind` → `server/handles.UpdateSharingReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "must add at least 1 object", 400)
common.ErrorStrResp(c, "no such a user", 400)
common.ErrorStrResp(c, "permission denied", 403)
common.ErrorStrResp(c, fmt.Sprintf("permission denied to share path [%s]", s), 500)
common.ErrorStrResp(c, "sharing not found", 404)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, SharingResp{
	Sharing:	s,
	CreatorName:	s.Creator.Username,
	CreatorRole:	s.Creator.Role,
})
```

本地后续调用：`server/handles.validateSharingID`。


### `server/handles.UpdateStorage`

源码：`server/handles/storage.go:113`。

请求绑定：`ShouldBind` → `internal/model.Storage`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c)
```


### `server/handles.UpdateUser`

源码：`server/handles/user.go:52`。

请求绑定：`ShouldBind` → `internal/model.User`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500)
common.ErrorStrResp(c, "role can not be changed", 400)
common.ErrorStrResp(c, "admin user can not be disabled", 400)
common.SuccessResp(c)
```


### `server/handles.UploadMediaItemCover`

源码：`server/handles/media_cover_upload.go:99`。

直接参数：form `id`; form `discard`; file `file`; form `deferred`。

```go
common.ErrorStrResp(c, "无效的 id", 400)
common.ErrorStrResp(c, "无效的暂存封面", 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c, nil)
common.ErrorResp(c, err, 404)
common.ErrorStrResp(c, "缺少 file 文件参数", 400)
common.ErrorStrResp(c, "图片过大（上限 8MB）", 400)
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "读取上传文件失败", 400)
common.ErrorStrResp(c, "图片为空或超过 8MB", 400)
common.ErrorStrResp(c, err.Error(), 400)
common.SuccessResp(c, gin.H{"cover": cover})
```

本地后续调用：`server/handles.removeUploadedCover`, `server/handles.saveUploadedCover`, `server/handles.thumbsDirName`。


### `server/handles.UploadSeedAndParse`

源码：`server/handles/torrent.go:924`。

直接参数：file `seed`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "seed file is too large", 400)
common.ErrorStrResp(c, "failed to read bounded seed file", 400)
common.SuccessResp(c, gin.H{
	"format":	format,
	"seed":		seed,
	"seed_data":	base64.StdEncoding.EncodeToString(data),
	"diagnostics":	seedDiagnostics(seed),
})
```

本地后续调用：`server/handles.seedDiagnostics`。


### `server/handles.UploadTorrentAndParse`

源码：`server/handles/torrent.go:552`。

直接参数：file `torrent`。

```go
common.ErrorResp(c, fmt.Errorf("failed to read the uploaded file: %w", err), 400)
common.ErrorResp(c, fmt.Errorf("torrent file is too large (max 10MB)"), 400)
common.ErrorResp(c, fmt.Errorf("failed to open the file: %w", err), 500)
common.ErrorResp(c, fmt.Errorf("failed to read the file: %w", err), 500)
common.ErrorResp(c, fmt.Errorf("failed to parse torrent: %w", err), 400)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, gin.H{
	"info":		resp,
	"torrent_data":	base64.StdEncoding.EncodeToString(torrentData),
})
```

本地后续调用：`server/handles.validateParsedTorrent`。


### `server/handles.Verify2FA`

源码：`server/handles/auth.go:183`。

请求绑定：`ShouldBind` → `server/handles.Verify2FAReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, model.GuestCannotGenerate2FA, 403)
common.ErrorStrResp(c, model.Invalid2FACode, 400)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```


### `server/handles.WorkerClaim`

源码：`server/handles/transcode.go:518`。

请求绑定：`ShouldBindJSON` → `internal/transcode.ClaimRequest`。

```go
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, transcode.ClaimResponse{Jobs: jobs})
```

本地后续调用：`server/handles.checkWorkerSecret`。


### `server/handles.WorkerHeartbeat`

源码：`server/handles/transcode.go:498`。

请求绑定：`ShouldBindJSON` → `internal/transcode.HeartbeatRequest`。

```go
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, transcode.HeartbeatResponse{OK: false, Kick: true})
common.SuccessResp(c, transcode.HeartbeatResponse{OK: true, Cancel: cancellations})
```

本地后续调用：`server/handles.checkWorkerSecret`。


### `server/handles.WorkerJobFinish`

源码：`server/handles/transcode.go:585`。

请求绑定：`ShouldBindJSON` → `internal/transcode.FinishRequest`。

直接参数：header `Authorization`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "job not found", 404)
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorResp(c, err, 500)
common.SuccessResp(c)
```

本地后续调用：`server/handles.tokenMatches`。


### `server/handles.WorkerRegister`

源码：`server/handles/transcode.go:474`。

请求绑定：`ShouldBindJSON` → `internal/transcode.RegisterRequest`。

```go
common.ErrorStrResp(c, "transcode disabled", 403)
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorResp(c, err, 400)
common.SuccessResp(c, transcode.RegisterResponse{
	WorkerID:		w.ID,
	HeartbeatInterval:	10,
	ClaimStrategy:		"pull",
	ProtocolVersion:	transcode.ProtocolVersion,
})
```

本地后续调用：`server/handles.checkWorkerSecret`。


### `server/handles.WorkerSegmentUpload`

源码：`server/handles/transcode.go:537`。

直接参数：query `job`; query `profile`; query `seq`; query `duration`; query `final`; header `Authorization`。

```go
common.ErrorStrResp(c, "job not found", 404)
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorStrResp(c, "invalid profile", 400)
common.ErrorStrResp(c, "profile not declared for this job", 400)
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 413)
common.ErrorResp(c, err, 500)
c.Status(http.StatusNoContent)
```

本地后续调用：`server/handles.jobHasProfile`。


### `server/handles._keyState`

源码：`server/handles/ssologin.go:48`。


### `server/handles.add115OfflineDownloads`

源码：`server/handles/offline_download.go:398`。

```go
common.SuccessResp(c, data)
common.ErrorWithDataResp(c, errors.New("no offline download URLs provided"), 400, data)
common.ErrorWithDataResp(c, firstErr, 500, data)
```

本地后续调用：`server/handles.getTaskInfos`。


### `server/handles.applyChannelUpdate`

源码：`server/handles/torrent.go:2142`。

本地后续调用：`server/handles.containsString`, `server/handles.removeString`。


### `server/handles.applySeedMatrix`

源码：`server/handles/torrent.go:1281`。


### `server/handles.applySeedUpdateOptions`

源码：`server/handles/torrent.go:1584`。


### `server/handles.argsContains`

源码：`server/handles/task.go:74`。


### `server/handles.atoiDefault`

源码：`server/handles/media_user.go:256`。


### `server/handles.autoRegister`

源码：`server/handles/ssologin.go:281`。


### `server/handles.buildExportFilename`

源码：`server/handles/media.go:1198`。


### `server/handles.buildSeedFileRapidUploadRequest`

源码：`server/handles/torrent.go:126`。


### `server/handles.buildSeedRapidUploadRequest`

源码：`server/handles/torrent.go:50`。


### `server/handles.canAccessTask`

源码：`server/handles/task.go:86`。


### `server/handles.checkMediaZipEntryBudget`

源码：`server/handles/media_export.go:455`。


### `server/handles.checkRelativePath`

源码：`server/handles/fsmanage.go:362`。


### `server/handles.checkSeedShareValidity`

源码：`server/handles/torrent.go:2120`。


### `server/handles.checkWorkerSecret`

源码：`server/handles/transcode.go:652`。

直接参数：header `Authorization`。


### `server/handles.clampMediaPage`

源码：`server/handles/media.go:227`。


### `server/handles.clampMergeDepth`

源码：`server/handles/media.go:115`。


### `server/handles.cleanupMediaImportedCovers`

源码：`server/handles/media.go:1294`。


### `server/handles.configuredSeedFormats`

源码：`server/handles/fsup.go:353`。


### `server/handles.containsString`

源码：`server/handles/torrent.go:2188`。


### `server/handles.convertSeedToFormat`

源码：`server/handles/torrent.go:1158`。

本地后续调用：`server/handles.applySeedMatrix`, `server/handles.seedConvertMatrix`, `server/handles.seedConvertMatrixMissing`。


### `server/handles.coverBytes`

源码：`server/handles/media_export.go:53`。

本地后续调用：`server/handles.fetchCover`, `server/handles.mediaExportThumbPath`, `server/handles.rasterCoverExt`, `server/handles.safeCoverURL`, `server/handles.thumbsDirName`。


### `server/handles.coverFileExt`

源码：`server/handles/media_cover_upload.go:35`。


### `server/handles.dealError`

源码：`server/handles/sharing.go:317`。

```go
common.ErrorStrResp(c, "the share does not exist", 500)
common.ErrorStrResp(c, "the share has expired or is no longer valid", 500)
common.ErrorResp(c, err, 403)
common.ErrorResp(c, err, 202)
common.ErrorResp(c, err, 500)
```


### `server/handles.decodeSeedData`

源码：`server/handles/torrent.go:868`。


### `server/handles.defaultMediaImportLimits`

源码：`server/handles/media_export.go:330`。


### `server/handles.deleteManagedCloudTask`

源码：`server/handles/task_delete.go:57`。

本地后续调用：`server/handles.taskOperationCode`。


### `server/handles.directSourceAvailable`

源码：`server/handles/torrent.go:1404`。

本地后续调用：`server/handles.isEncrypt`。


### `server/handles.effectivePort`

源码：`server/handles/torrent.go:284`。


### `server/handles.encodePath`

源码：`server/handles/transcode.go:235`。


### `server/handles.exifString`

源码：`server/handles/media_exif.go:183`。


### `server/handles.exifTagRat`

源码：`server/handles/media_exif.go:205`。


### `server/handles.fetchCover`

源码：`server/handles/media_image.go:120`。

本地后续调用：`server/handles.safeCoverURL`。


### `server/handles.fetchCovers`

源码：`server/handles/media_export.go:189`。

本地后续调用：`server/handles.fetchCoversWithBudget`。


### `server/handles.fetchCoversWithBudget`

源码：`server/handles/media_export.go:193`。

本地后续调用：`server/handles.coverBytes`。


### `server/handles.filterDirs`

源码：`server/handles/fsread.go:175`。


### `server/handles.filterRelated`

源码：`server/handles/fsread.go:384`。


### `server/handles.firstUsableSeedSource`

源码：`server/handles/torrent.go:2329`。

本地后续调用：`server/handles.seedSiteConfig`, `server/handles.validateSeedHost`。


### `server/handles.followSeedRemove`

源码：`server/handles/fsmanage.go:493`。

本地后续调用：`server/handles.seedSidecarPaths`。


### `server/handles.followSeedRename`

源码：`server/handles/fsmanage.go:475`。

本地后续调用：`server/handles.seedSidecarPaths`。


### `server/handles.followSeedTransfer`

源码：`server/handles/fsmanage.go:445`。

本地后续调用：`server/handles.seedSidecarPaths`。


### `server/handles.formatAperture`

源码：`server/handles/media_exif.go:229`。

本地后续调用：`server/handles.exifTagRat`, `server/handles.trimFloat`。


### `server/handles.formatExposure`

源码：`server/handles/media_exif.go:218`。

本地后续调用：`server/handles.exifTagRat`, `server/handles.trimFloat`。


### `server/handles.formatFocal`

源码：`server/handles/media_exif.go:237`。

本地后续调用：`server/handles.exifTagRat`, `server/handles.trimFloat`。


### `server/handles.formatTakenAt`

源码：`server/handles/media_exif.go:251`。


### `server/handles.generateSSOBindingToken`

源码：`server/handles/ssologin.go:91`。

本地后续调用：`server/handles.ssoBindingSessionDigest`。


### `server/handles.generateState`

源码：`server/handles/ssologin.go:52`。

本地后续调用：`server/handles._keyState`。


### `server/handles.getBatchHandler`

源码：`server/handles/task.go:141`。

请求绑定：`ShouldBind` → `[]string`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "invalid request format", 400)
common.SuccessResp(c, retErrs)
```

本地后续调用：`server/handles.canAccessTask`, `server/handles.getUserInfo`。


### `server/handles.getClearTasksHandler`

源码：`server/handles/task_delete.go:168`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "user invalid", 401)
common.SuccessResp(c, gin.H{"results": results})
common.SuccessResp(c)
```

本地后续调用：`server/handles.argsContains`, `server/handles.canAccessTask`, `server/handles.deleteManagedCloudTask`, `server/handles.getUserInfo`, `server/handles.parseDeleteCloudRecord`, `server/handles.removeManagedTask`, `server/handles.taskOperationError`。


### `server/handles.getDeleteSomeTasksHandler`

源码：`server/handles/task_delete.go:128`。

请求绑定：`ShouldBind` → `[]string`。

```go
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "invalid request format", 400)
common.SuccessResp(c, gin.H{"results": results})
```

本地后续调用：`server/handles.canAccessTask`, `server/handles.deleteManagedCloudTask`, `server/handles.getBatchHandler`, `server/handles.getUserInfo`, `server/handles.missingTaskDeleteResult`, `server/handles.parseDeleteCloudRecord`, `server/handles.removeManagedTask`。


### `server/handles.getDeleteTaskHandler`

源码：`server/handles/task_delete.go:95`。

直接参数：query `tid`。

```go
common.SuccessResp(c)
common.ErrorResp(c, err, 400)
common.ErrorStrResp(c, "user invalid", 401)
```

本地后续调用：`server/handles.canAccessTask`, `server/handles.deleteManagedCloudTask`, `server/handles.getTargetedHandler`, `server/handles.getUserInfo`, `server/handles.missingTaskDeleteResult`, `server/handles.parseDeleteCloudRecord`, `server/handles.removeManagedTask`, `server/handles.respondTaskDeletion`, `server/handles.taskOperationError`。


### `server/handles.getHeader`

源码：`server/handles/fsread.go:195`。


### `server/handles.getLastModified`

源码：`server/handles/fsup.go:28`。

直接参数：header `Last-Modified`。


### `server/handles.getReadme`

源码：`server/handles/fsread.go:188`。


### `server/handles.getTargetedHandler`

源码：`server/handles/task.go:119`。

直接参数：query `tid`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.ErrorStrResp(c, "task not found", 404)
```

本地后续调用：`server/handles.canAccessTask`, `server/handles.getUserInfo`。


### `server/handles.getTaskInfo`

源码：`server/handles/task.go:35`。


### `server/handles.getTaskInfos`

源码：`server/handles/task.go:70`。


### `server/handles.getUserInfo`

源码：`server/handles/task.go:78`。


### `server/handles.hasAnyScrapedField`

源码：`server/handles/media.go:871`。


### `server/handles.hostAllowed`

源码：`server/handles/media_image.go:33`。


### `server/handles.isEncrypt`

源码：`server/handles/fsread.go:202`。


### `server/handles.isSearchNodeAccessible`

源码：`server/handles/search.go:59`。


### `server/handles.isZipPayload`

源码：`server/handles/media_export.go:563`。


### `server/handles.jobHasProfile`

源码：`server/handles/transcode.go:632`。


### `server/handles.joinNonEmpty`

源码：`server/handles/media_exif.go:195`。


### `server/handles.list`

源码：`server/handles/sshkey.go:111`。

请求绑定：`ShouldBind` → `internal/model.PageReq`。

```go
common.ErrorResp(c, err, 400)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, common.PageResp{
	Content:	keys,
	Total:		total,
})
```


### `server/handles.loadImageExif`

源码：`server/handles/media_exif.go:96`。

本地后续调用：`server/handles.exifString`, `server/handles.formatAperture`, `server/handles.formatExposure`, `server/handles.formatFocal`, `server/handles.formatTakenAt`, `server/handles.joinNonEmpty`, `server/handles.readMediaHead`。


### `server/handles.loadSeedDefaultMatrix`

源码：`server/handles/torrent.go:1252`。


### `server/handles.loadSeedDefaultTrackers`

源码：`server/handles/torrent.go:1388`。


### `server/handles.loginHash`

源码：`server/handles/auth.go:47`。

```go
common.ErrorStrResp(c, model.TooManyAttempts, 429)
common.ErrorStrResp(c, model.InvalidUsernameOrPassword, 401)
common.ErrorStrResp(c, model.Invalid2FACode, 402)
common.ErrorResp(c, err, 500, true)
common.SuccessResp(c, gin.H{"token": token})
```


### `server/handles.makeStorageResp`

源码：`server/handles/storage.go:31`。


### `server/handles.mediaAnnotationTarget`

源码：`server/handles/media_reader.go:23`。

```go
common.ErrorStrResp(c, "unauthorized", 401)
common.ErrorStrResp(c, "guest cannot save annotations", 403)
common.ErrorStrResp(c, "invalid request", 400)
common.ErrorStrResp(c, "not found", 404)
```

本地后续调用：`server/handles.CanUserAccessItem`, `server/handles.mediaUser`。


### `server/handles.mediaExportThumbPath`

源码：`server/handles/media_export.go:131`。

本地后续调用：`server/handles.mediaPathWithin`, `server/handles.safeMediaRelativePath`, `server/handles.thumbsDirName`。


### `server/handles.mediaPathWithin`

源码：`server/handles/media_export.go:160`。


### `server/handles.mediaUser`

源码：`server/handles/media_gate.go:56`。


### `server/handles.minFloat`

源码：`server/handles/media_user.go:263`。


### `server/handles.missingTaskDeleteResult`

源码：`server/handles/task_delete.go:51`。


### `server/handles.multipartChunkSize`

源码：`server/handles/multipart.go:29`。


### `server/handles.multipartErrCode`

源码：`server/handles/multipart.go:219`。


### `server/handles.normalizedSeedMatrix`

源码：`server/handles/torrent.go:1260`。

本地后续调用：`server/handles.loadSeedDefaultMatrix`, `server/handles.seedMatrixEmpty`。


### `server/handles.pagination`

源码：`server/handles/fsread.go:215`。


### `server/handles.parseDeleteCloudRecord`

源码：`server/handles/task_delete.go:24`。


### `server/handles.parseJWT`

源码：`server/handles/ssologin.go:311`。


### `server/handles.parseSSOBindingToken`

源码：`server/handles/ssologin.go:68`。

本地后续调用：`server/handles.ssoBindingSessionDigest`。


### `server/handles.prepareMediaScrapeCandidate`

源码：`server/handles/media.go:860`。


### `server/handles.rasterCoverExt`

源码：`server/handles/media_export.go:166`。

本地后续调用：`server/handles.coverFileExt`。


### `server/handles.readMediaExportZipWithBudget`

源码：`server/handles/media_export.go:334`。

本地后续调用：`server/handles.checkMediaZipEntryBudget`, `server/handles.rasterCoverExt`, `server/handles.readMediaZipEntry`, `server/handles.safeMediaRelativePath`, `server/handles.thumbsDirName`。


### `server/handles.readMediaHead`

源码：`server/handles/media_exif.go:144`。

本地后续调用：`server/handles.MediaItemFilePath`。


### `server/handles.readMediaImportFile`

源码：`server/handles/media.go:1256`。

本地后续调用：`server/handles.defaultMediaImportLimits`, `server/handles.isZipPayload`, `server/handles.readMediaExportZipWithBudget`。


### `server/handles.readMediaImportRequest`

源码：`server/handles/media.go:1219`。

直接参数：header `Content-Type`; file `file`。

本地后续调用：`server/handles.readMediaImportFile`。


### `server/handles.readMediaZipEntry`

源码：`server/handles/media_export.go:531`。


### `server/handles.rehashSeedFile`

源码：`server/handles/torrent.go:1624`。


### `server/handles.removeManagedTask`

源码：`server/handles/task.go:91`。


### `server/handles.removeString`

源码：`server/handles/torrent.go:2197`。


### `server/handles.removeUploadedCover`

源码：`server/handles/media_cover_upload.go:69`。

本地后续调用：`server/handles.thumbsDirName`。


### `server/handles.resolveLocalFilePath`

源码：`server/handles/transcode.go:263`。


### `server/handles.resolveSeedTransitPath`

源码：`server/handles/torrent.go:2006`。


### `server/handles.respondTaskDeletion`

源码：`server/handles/task_delete.go:87`。

```go
common.ErrorWithDataResp(c, errors.New(result.Error), result.Code, result)
common.SuccessResp(c, result)
```


### `server/handles.respondTranscodeJob`

源码：`server/handles/transcode.go:221`。

```go
common.SuccessResp(c, gin.H{
	"transcode":	true,
	"job_id":	job.ID,
	"master_url":	masterURL,
	"profile":	profileName,
})
```


### `server/handles.retryManagedTask`

源码：`server/handles/task.go:101`。


### `server/handles.safeCoverURL`

源码：`server/handles/media_image.go:51`。

本地后续调用：`server/handles.hostAllowed`。


### `server/handles.safeMediaRelativePath`

源码：`server/handles/media_export.go:118`。


### `server/handles.sameSeedHost`

源码：`server/handles/torrent.go:274`。

本地后续调用：`server/handles.effectivePort`。


### `server/handles.saveAndInitOfflineDownloadTool`

源码：`server/handles/offline_download.go:17`。

```go
common.ErrorResp(c, err, 500)
```


### `server/handles.saveSeedFilesToPath`

源码：`server/handles/torrent.go:1859`。

本地后续调用：`server/handles.buildSeedFileRapidUploadRequest`, `server/handles.firstUsableSeedSource`, `server/handles.getTaskInfo`, `server/handles.seedContentOpener`, `server/handles.seedFileSupportsRapidUpload`, `server/handles.seedRapidUploadBlocker`, `server/handles.seedRapidUploaderFor`。


### `server/handles.saveUploadedCover`

源码：`server/handles/media_cover_upload.go:50`。

本地后续调用：`server/handles.coverFileExt`, `server/handles.thumbsDirName`。


### `server/handles.seedContentOpener`

源码：`server/handles/torrent.go:2275`。

本地后续调用：`server/handles.firstUsableSeedSource`。


### `server/handles.seedConversionStates`

源码：`server/handles/torrent.go:2378`。


### `server/handles.seedConvertMatrix`

源码：`server/handles/torrent.go:1198`。


### `server/handles.seedConvertMatrixMissing`

源码：`server/handles/torrent.go:1212`。


### `server/handles.seedConvertTargets`

源码：`server/handles/torrent.go:1125`。


### `server/handles.seedDiagnostics`

源码：`server/handles/torrent.go:2365`。


### `server/handles.seedFileSupportsRapidUpload`

源码：`server/handles/torrent.go:184`。


### `server/handles.seedMatrixEmpty`

源码：`server/handles/torrent.go:1247`。


### `server/handles.seedPieceSize`

源码：`server/handles/fsup.go:308`。

直接参数：header `X-Seed-Piece-Size`。


### `server/handles.seedRapidUploadBlocker`

源码：`server/handles/torrent.go:174`。


### `server/handles.seedRapidUploaderFor`

源码：`server/handles/torrent.go:37`。


### `server/handles.seedSaveErrorCode`

源码：`server/handles/torrent.go:2104`。


### `server/handles.seedSaveResultsError`

源码：`server/handles/torrent.go:2250`。


### `server/handles.seedSidecarFormats`

源码：`server/handles/fsup.go:322`。

直接参数：header `X-Seed-Sidecars`; header `X-Seed-Format`。

本地后续调用：`server/handles.configuredSeedFormats`。


### `server/handles.seedSidecarPaths`

源码：`server/handles/fsmanage.go:441`。


### `server/handles.seedSidecarWantsCAS`

源码：`server/handles/fsup.go:334`。

本地后续调用：`server/handles.seedSidecarFormats`。


### `server/handles.seedSiteConfig`

源码：`server/handles/torrent.go:253`。


### `server/handles.seedTotalSize`

源码：`server/handles/torrent.go:2357`。


### `server/handles.selectSeedConvertFiles`

源码：`server/handles/torrent.go:1094`。


### `server/handles.setSafeImageHeaders`

源码：`server/handles/media_image.go:112`。

```go
c.Header("X-Content-Type-Options", "nosniff")
c.Header("Content-Disposition", `inline; filename="cover"`)
c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
```


### `server/handles.shouldGenerateUploadSeed`

源码：`server/handles/fsup.go:291`。

直接参数：header `X-Seed-Sidecars`; header `X-Generate-Seed`。

本地后续调用：`server/handles.configuredSeedFormats`。


### `server/handles.shouldIgnoreSystemFile`

源码：`server/handles/fsup.go:40`。


### `server/handles.ssoBindingSessionDigest`

源码：`server/handles/ssologin.go:63`。


### `server/handles.ssoPostMessage`

源码：`server/handles/ssologin.go:150`。

```go
common.ErrorResp(c, err, 500)
c.Data(200, "text/html; charset=utf-8", []byte(html))
```

本地后续调用：`server/handles.ssoTargetOrigin`。


### `server/handles.ssoRedirectUri`

源码：`server/handles/ssologin.go:172`。


### `server/handles.ssoTargetOrigin`

源码：`server/handles/ssologin.go:130`。


### `server/handles.taskOperationCode`

源码：`server/handles/task_delete.go:44`。


### `server/handles.taskOperationError`

源码：`server/handles/task.go:115`。

```go
common.ErrorResp(c, err, taskOperationCode(err))
```

本地后续调用：`server/handles.taskOperationCode`。


### `server/handles.taskRoute/done`

源码：`server/handles/task.go:184`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {
	return canAccessTask(task, isAdmin, uid) &&
		argsContains(task.GetState(), tache.StateCanceled, tache.StateFailed, tache.StateSucceeded)
})))
```

本地后续调用：`server/handles.argsContains`, `server/handles.canAccessTask`, `server/handles.getTaskInfos`, `server/handles.getUserInfo`。


### `server/handles.taskRoute/retry_failed`

源码：`server/handles/task.go:226`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.SuccessResp(c)
```

本地后续调用：`server/handles.canAccessTask`, `server/handles.getUserInfo`, `server/handles.retryManagedTask`, `server/handles.taskOperationError`。


### `server/handles.taskRoute/undone`

源码：`server/handles/task.go:170`。

```go
common.ErrorStrResp(c, "user invalid", 401)
common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {

	return canAccessTask(task, isAdmin, uid) &&
		argsContains(task.GetState(), tache.StatePending, tache.StateRunning, tache.StateCanceling,
			tache.StateErrored, tache.StateFailing, tache.StateWaitingRetry, tache.StateBeforeRetry)
})))
```

本地后续调用：`server/handles.argsContains`, `server/handles.canAccessTask`, `server/handles.getTaskInfos`, `server/handles.getUserInfo`。


### `server/handles.thumbnailRoot`

源码：`server/handles/media_image.go:147`。


### `server/handles.thumbsDirName`

源码：`server/handles/media_cover_upload.go:26`。


### `server/handles.toContentResp`

源码：`server/handles/archive.go:61`。

本地后续调用：`server/handles.toContentResp`, `server/handles.toObjsRespWithoutSignAndThumb`。


### `server/handles.toObjsResp`

源码：`server/handles/fsread.go:229`。


### `server/handles.toObjsRespWithoutSignAndThumb`

源码：`server/handles/archive.go:46`。


### `server/handles.toSeedGenerateParams`

源码：`server/handles/torrent.go:1310`。


### `server/handles.tokenMatches`

源码：`server/handles/transcode.go:645`。


### `server/handles.transferSeedViaTransit`

源码：`server/handles/torrent.go:2019`。

```go
common.ErrorResp(c, err, code)
common.SuccessResp(c, gin.H{"results": finalResults})
```

本地后续调用：`server/handles.saveSeedFilesToPath`, `server/handles.seedSaveErrorCode`。


### `server/handles.trimFloat`

源码：`server/handles/media_exif.go:245`。


### `server/handles.trustedInternalBaseURL`

源码：`server/handles/transcode.go:247`。


### `server/handles.uploadSeedSliceSize`

源码：`server/handles/fsup.go:346`。

本地后续调用：`server/handles.seedPieceSize`, `server/handles.seedSidecarWantsCAS`。


### `server/handles.userPathPrefix`

源码：`server/handles/media_gate.go:87`。


### `server/handles.validHide`

源码：`server/handles/meta.go:71`。


### `server/handles.validateJSONArray`

源码：`server/handles/media.go:312`。


### `server/handles.validateOfflineDownloadStorage`

源码：`server/handles/offline_download.go:35`。

```go
common.ErrorStrResp(c, "storage does not exists", 400)
common.ErrorStrResp(c, "storage not init: "+storage.GetStorage().Status, 400)
common.ErrorStrResp(c, "unsupported storage driver for offline download, only "+nativeTool+" is supported", 400)
```


### `server/handles.validateParsedTorrent`

源码：`server/handles/torrent.go:321`。


### `server/handles.validateSeedHost`

源码：`server/handles/torrent.go:300`。

本地后续调用：`server/handles.sameSeedHost`, `server/handles.seedSiteConfig`。


### `server/handles.validateSeedSource`

源码：`server/handles/torrent.go:1601`。

本地后续调用：`server/handles.validateSeedHost`。


### `server/handles.validateSharingID`

源码：`server/handles/sharing.go:422`。


### `server/handles.validateUploadSeedRequest`

源码：`server/handles/fsup.go:370`。

直接参数：header `X-Seed-Hash-Matrix`。

本地后续调用：`server/handles.seedSidecarFormats`。


### `server/handles.verifyState`

源码：`server/handles/ssologin.go:58`。

本地后续调用：`server/handles._keyState`。


### `server/handles.writeMediaExportZip`

源码：`server/handles/media_export.go:257`。

本地后续调用：`server/handles.fetchCovers`。


### `server/handles.writeUploadSeedSidecar`

源码：`server/handles/fsup.go:383`。

直接参数：header `X-Seed-Hash-Matrix`。

本地后续调用：`server/handles.applySeedMatrix`, `server/handles.getLastModified`, `server/handles.normalizedSeedMatrix`, `server/handles.seedSidecarFormats`, `server/handles.uploadSeedSliceSize`。
