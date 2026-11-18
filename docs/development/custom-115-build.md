# 个人 115 版本构建

个人前端源码位于 [XQY-A/OpenList-Frontend](https://github.com/XQY-A/OpenList-Frontend) 的 `custom/115` 分支。后端使用 `build-config/custom-115-frontend.conf` 中的**完整提交 SHA**，从该版本构建并嵌入前端。

本分支的个人构建入口是 `build-custom-115.sh` 和 `Dockerfile.custom-115`。上游 `build.sh`、`Dockerfile` 保持原有发布用途，会下载官方前端；构建个人界面时使用下面的命令。

## 本地二进制

环境需要 Git、Go（遵循 `go.mod` 的 toolchain）、Node.js 和 `pnpm@11.28.11`。CI 使用 Node.js `24.22.2`。

```sh
bash build-custom-115.sh
./bin/openlist version
```

默认下载配置中固定的前端提交，在临时目录执行冻结依赖安装和固定语言包构建，再生成当前平台的二进制。语言包版本及 SHA256 在前端的 `scripts/i18n-lock.json` 中锁定；中文新增文案来自前端受跟踪的 `src/lang-overrides/zh-CN/`。下载或校验失败会终止构建。

已有对应前端 checkout 时，可以复用它：

```sh
bash build-custom-115.sh --frontend-dir ../openlist-frontend
```

指定目录必须是 Git 仓库根目录、HEAD 等于固定提交，且没有未提交或未跟踪改动。构建前后都会校验，产物中的 `SOURCE_COMMIT` 必须匹配、`SOURCE_DIRTY` 必须为 `false`。个人规则和计划应使用本地 Git 忽略配置，不加入源码提交。

默认生成：

- `bin/openlist`：包含个人前端的可执行文件。
- `bin/openlist.build-info`：前后端完整 SHA、前端版本、后端工作区是否有未提交修改及构建时间。
- `bin/openlist.sha256`：二进制 SHA256。

`--output PATH` 可指定二进制输出路径。带有后端未提交修改的本地预览会在版本中标记 `-dirty`，构建记录也会写入 `backend_dirty=true`；该预览不代表一个固定的源码快照。交付时应从干净提交构建，并核对构建记录。`public/dist` 是生成目录，不需要提交。

运行配置中的 `dist_dir` 留空时使用嵌入前端；非空时会优先加载该外部目录。升级后应核对这项配置，避免已有的外部前端目录覆盖个人构建中的界面。

编译和回执生成完成后才替换二进制、构建记录与校验值；失败时恢复原有产物和嵌入前端目录。输出路径必须为普通文件，不覆盖同名目录或符号链接。

## Docker

从后端 checkout 执行包装脚本，让它记录实际后端版本和工作区状态：

```sh
bash build-custom-115.sh --docker --image openlist:custom-115
docker run --rm --entrypoint /entrypoint.sh openlist:custom-115 version
```

Docker 构建不要求宿主机安装 Go、Node.js 或 pnpm。镜像内仍使用同一完整前端 SHA，冻结安装依赖并校验固定语言包；Go 构建使用与上游 Alpine 运行镜像匹配的静态链接方式。镜像内的 `/opt/openlist/openlist.build-info` 可用于核对版本。

`Dockerfile.custom-115.dockerignore` 排除本地数据、Git 元数据、个人计划及已有构建产物。直接调用 Dockerfile 的构建系统必须传入准确的 `BACKEND_COMMIT` 和 `BACKEND_DIRTY`；通常应使用包装脚本，避免人工填写来源信息。

部署沿用 OpenList 的数据卷和端口配置；替换已有实例前先停机备份数据目录，具体升级与功能使用见 [115 Open 离线下载 API](../api/115-open-offline.md)。仅执行构建命令不会启动服务或修改现有数据。

## 从公共 GHCR 镜像部署

个人镜像发布到 `ghcr.io/xqy-a/openlist`，同一标签包含 `linux/amd64` 和 `linux/arm64`，Docker 会按设备架构选择对应镜像。包设为公开后，无需 GitHub 账号或 `docker login` 即可拉取：

```sh
docker pull ghcr.io/xqy-a/openlist:custom-115
```

设备只支持从镜像仓库拉取时，在其容器管理页面填写以下配置，无需导入本地镜像：

| 配置 | 值 |
| --- | --- |
| 镜像 | `ghcr.io/xqy-a/openlist:custom-115` |
| 容器端口 | `5244/TCP`，映射到设备上可用的访问端口 |
| 持久化目录 | 将设备上的持久化数据目录挂载到容器 `/opt/openlist/data` |
| 运行用户 | 镜像默认使用 UID `1001`、GID `1001`；挂载目录需允许该用户读写 |

容器启动后通过 `http://设备地址:映射端口` 访问。更新镜像时保留数据挂载；迁移已有实例时先停机备份，并避免两个实例同时使用同一数据目录。镜像需要设备能访问 `ghcr.io`。

发布标签的含义：

| 标签或引用 | 用途 |
| --- | --- |
| `custom-115` | 滚动标签，跟随个人分支最新一次成功发布 |
| `sha-<完整后端提交 SHA>` | 按后端源码版本查找镜像；同一提交重新构建时会更新此标签 |
| `build-<run_id>-<run_attempt>` | 对应一次 GitHub Actions 发布尝试，便于定位构建记录 |
| `ghcr.io/xqy-a/openlist@sha256:<索引摘要>` | 精确指定某次发布的多架构镜像，用于固定部署和回滚 |

升级前记录当前部署的镜像 digest。回滚时将设备中的镜像地址改为保存的 digest 引用，例如：

```sh
docker pull ghcr.io/xqy-a/openlist@sha256:<保存的完整索引摘要>
```

完整后端 SHA 能确定源码版本，但同一提交再次构建不保证得到同一镜像 digest；构建时间和基础镜像等因素也会影响产物。需要准确还原某次发布时使用其 digest，并保留对应的 registry 版本。镜像回滚不恢复数据目录，数据兼容与备份要求见 [115 Open 使用与升级说明](../usage/115-open-offline.md)。镜像内的 `/opt/openlist/openlist.build-info` 和 OCI revision 标签可用于核对后端版本及固定的前端版本。

### 首次公开包

GHCR 首次创建的个人包默认是 private，即使源码仓库已经公开也不会自动变为 public。首次工作流推送成功后，仓库所有者需要在 GitHub 个人主页的 **Packages → openlist → Package settings → Danger Zone → Change visibility → Public** 中输入包名确认。公开后应使用未登录的客户端拉取镜像，确认设备也能匿名获取；已有 Docker 登录状态的拉取成功不能证明包已公开。

GitHub 当前公开 REST Packages API 没有修改可见性的端点，因此这一步通过包设置页面完成。GitHub 不支持将已公开的包改回 private。详细权限规则见 [GitHub 官方包访问权限说明](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)。

## CI 与更新前端

前后端手动上游同步、共享任务变更的审查和发布见 [个人 115 上游手动同步](custom-115-sync.md)。两个个人仓库将默认分支设为 `custom/115` 后，可在 Actions 网页直接手动运行个人工作流。

前后端的 `Personal 115 Build` 工作流都监听 `custom/115` 的推送和 PR，并支持手动触发及同步流程调用。GitHub 的网页手动入口要求工作流存在于默认分支。前端执行类型检查、单元测试及固定语言包构建；后端执行测试、并发检查、固定前端构建及 `linux/amd64`、`linux/arm64` Docker 构建，并上传构建 artifact。

在 `XQY-A/OpenList` 的 `custom/115` 分支推送或手动触发时，后端工作流使用 `GITHUB_TOKEN` 的 `packages: write` 权限，将通过验证的镜像推送到 GHCR，再合并为同一多架构索引，发布上文的三个标签。PR 只构建和验证，不登录 GHCR 或发布镜像。工作流不会自动发布 GitHub Release 或部署实例。首次通过仓库工作流发布的包会自动关联该仓库；镜像的 OCI source 标签同样指向个人后端仓库。详见 [GitHub 官方容器发布说明](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)。

更新前端时：

1. 在前端完成验证、提交并推送，核对远端完整 SHA。
2. 将后端 `build-config/custom-115-frontend.conf` 更新为该 SHA。
3. 执行构建并核对 `SOURCE_COMMIT`、`SOURCE_DIRTY` 和二进制版本，随后提交后端改动。

不把浮动分支名、`edge` 或 `latest` 写入前端提交配置。

构建校验测试可单独执行：

```sh
python3 -B -m unittest discover -s tests -p 'test_custom_115_build.py' -v
```

上游 `pkg/aria2/rpc` 测试需要 `localhost:6800` 的 Aria2 RPC 服务。个人 CI 为其启动独立的暂停下载测试实例；本地跑 `go test ./...` 时也需要隔离测试服务，避免测试中的 `PauseAll` 操作影响正在使用的 Aria2。
