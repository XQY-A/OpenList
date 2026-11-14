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

## CI 与更新前端

前后端的 `Personal 115 Build` 工作流都监听 `custom/115` 的推送、PR 和手动触发。前端执行类型检查、单元测试及固定语言包构建；后端执行测试、并发检查、固定前端构建及 Docker 构建。工作流上传构建 artifact，不自动发布 Release 或部署实例。

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
