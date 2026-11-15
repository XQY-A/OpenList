# OpenList API 参考

本目录记录 `custom/115` 分支实际实现的接口，供 AI、客户端和脚本开发使用。

| 文件 | 用途 |
| --- | --- |
| [openlist-api.md](openlist-api.md) | **优先给 AI 阅读**。单文件中文指南、115 扩展完整合同、全量 `/api/` 清单、请求模型、响应构造和源码定位 |
| [openlist.openapi.json](openlist.openapi.json) | OpenAPI 3.1。标准 HTTP 方法、请求／响应 schema、认证和完整处理函数源码元数据 |
| [115-open-offline.md](115-open-offline.md) | 只关注 115 Open 离线下载时使用的独立合同 |
| [ai-guide.md](ai-guide.md) | 手工维护的阅读与调用指南源文件，已嵌入完整参考 |

完整清单覆盖当前常规运行模式注册的 `/api/` 接口，包含各任务类型及 `/api/admin/task/<类型>` 的兼容入口。其他协议的入口与认证见完整参考；WebDAV、S3、MCP、Emby/OpenSubsonic 的协议内容不作为 JSON REST API 展开。动态驱动数据使用开放 schema，并提供实际响应构造与处理函数源码，不能据此假定所有字段都固定。

包含本分支已实现的 115 修改：异步添加逐项回执、任务 `info_hash` 与提交／清理状态、可选云记录清理、批量删除逐项结果、恢复和未知结果的处理规则。接口文档依据源码和合同，不代表已完成真实 115 账号验收。

可向 AI 附上这段提示：

> 请以附件 openlist-api.md 中当前 custom/115 实现为准调用 OpenList。需要追踪动态响应或复杂条件时，读取 openlist.openapi.json 的 x-openlist-contracts。不要套用与当前分支冲突的旧接口，不要为获取 info_hash 重发添加请求，不要将业务码 200 等同于批量全部成功。

官方参考为 <https://fox.oplist.org/>，官方 AI 索引为 <https://fox.oplist.org/llms.txt>，其中没有本分支的个人 115 扩展。[仓库 AI 索引](../../llms.txt)指向这份参考。

更新业务接口后，在仓库根目录执行：

```sh
python3 scripts/export_api_docs.py
python3 scripts/export_api_docs.py --check
python3 -m unittest discover -s tests -p 'test_api_docs.py' -v
```

生成器仅读取源码，不启动 OpenList，不连接存储。复杂条件、第三方绑定或自定义序列化需要同时更新手工合同和生成器的对应描述。提交接口改动时应同步提交重新生成的两个文件；`--check` 会检查内容漂移。测试使用标准库 Go／Python，不要求安装额外依赖。
