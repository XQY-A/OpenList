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
