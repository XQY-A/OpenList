#!/usr/bin/env python3
"""Export the current /api routes and source contracts without running a server."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs/api"
ANY_METHODS = ("get", "post", "put", "patch", "head", "options", "delete", "connect", "trace")
HTTP_METHODS = tuple(m for m in ANY_METHODS if m not in ("connect", "trace"))


def schema_name(name):
    return re.sub(r"[^A-Za-z0-9_.-]", "_", name)


class Schemas:
    def __init__(self, definitions):
        self.definitions = definitions
        self.schemas = {}

    def ref(self, name):
        name = name.strip()
        if name.startswith("*"):
            return {"anyOf": [self.ref(name[1:]), {"type": "null"}]}
        if name == "[]byte" or name == "[]uint8":
            return {"type": "string", "contentEncoding": "base64"}
        if name.startswith("[]"):
            return {"type": ["array", "null"], "items": self.ref(name[2:])}
        if name.startswith("map["):
            depth = 1
            end = 4
            while depth and end < len(name):
                if name[end] == "[":
                    depth += 1
                elif name[end] == "]":
                    depth -= 1
                end += 1
            return {"type": ["object", "null"], "additionalProperties": self.ref(name[end:])}
        if name == "time.Time":
            return {"type": "string", "format": "date-time"}
        if name == "time.Duration" or name.endswith("/tache.State"):
            return {"type": "integer"}
        if name in ("string", "bool"):
            return {"type": "boolean" if name == "bool" else "string"}
        if re.fullmatch(r"u?int\d*|byte", name):
            return {"type": "integer"}
        if name in ("float32", "float64"):
            return {"type": "number"}
        if name not in self.definitions:
            return {"x-go-type": name}
        key = schema_name(name)
        if key not in self.schemas:
            self.schemas[key] = {}  # Break recursive schema references.
            d = self.definitions[name]
            s = {"x-go-type": name, "x-source": {k: d["source"][k] for k in ("file", "line")}}
            if d["type"] != "object":
                s.update(self.ref(d["type"]))
            else:
                properties, required = self.fields(name)
                s.update(type="object", properties=properties)
                if required:
                    s["required"] = required
            self.schemas[key] = s
        return {"$ref": "#/components/schemas/" + key}

    def fields(self, name, mode="json", seen=None):
        if name == "internal/model.DiskUsage" and mode == "json":
            # MarshalJSON defines the wire format, including a calculated field.
            return {key: {"type": "integer"} for key in ("total_space", "used_space", "free_space")}, []
        seen = set() if seen is None else seen
        if name in seen:
            return {}, []
        seen = seen | {name}
        props, required = {}, []
        for f in self.definitions.get(name.lstrip("*"), {}).get("fields", []):
            tag = f.get(mode, "").split(",")[0]
            if tag == "-":
                continue
            if f["embedded"] and not tag:
                more, needed = self.fields(f["type"].lstrip("*"), mode, seen)
                props.update(more)
                required.extend(needed)
                continue
            key = tag or f["name"]
            props[key] = self.ref(f["type"])
            if f["binding"]:
                props[key] = {**props[key], "x-go-binding": f["binding"]}
                if "required" in f["binding"].split(","):
                    required.append(key)
        return props, sorted(set(required))


def reachable(contracts, start):
    found, pending = {}, [start]
    while pending:
        key = pending.pop()
        if key in found or key not in contracts:
            continue
        found[key] = contracts[key]
        pending.extend(contracts[key]["calls"])
    return found


def unique(items):
    return list({json.dumps(item, sort_keys=True): item for item in items}.values())


def envelope(data):
    return {"type": "object", "required": ["code", "message", "data"], "properties": {"code": {"type": "integer"}, "message": {"type": "string"}, "data": {"anyOf": [data, {"type": "null"}]}}}


def typed_response(op, data):
    op["responses"]["200"]["content"]["application/json"]["schema"] = envelope(data)


def special_responses(op, handler):
    """Explicit wire contracts for handlers that do not always return JSON."""
    binary = {"schema": {"type": "string", "format": "binary"}}
    responses = op["responses"]
    if handler in ("PublicMediaProxy", "PublicMediaThumb"):
        content = {mime: binary for mime in ("image/jpeg", "image/png", "image/gif", "image/webp")}
        responses.clear()
        responses["200"] = {"description": "图片字节（无 JSON 信封）", "content": content}
        for status in (400, 403, 404, 415, *([502] if handler == "PublicMediaProxy" else [])):
            responses[str(status)] = {"description": "真实 HTTP 错误状态，无 JSON 响应体"}
    elif handler == "GetMediaStrm":
        responses["200"]["description"] = "成功为 STRM 纯文本附件；业务错误为 JSON 信封"
        responses["200"]["content"]["text/plain"] = {"schema": {"type": "string"}}
        responses["200"]["headers"] = {"Content-Disposition": {"schema": {"type": "string"}, "description": "成功时 attachment"}}
    elif handler == "ExportMediaDB":
        responses["200"]["description"] = "成功为 ZIP 附件（media_export.json 与 covers/）；业务错误为 JSON 信封"
        responses["200"]["content"]["application/zip"] = binary
        responses["200"]["headers"] = {"Content-Disposition": {"schema": {"type": "string"}, "description": "成功时 attachment"}}
    elif handler in ("SSOLoginRedirect", "SSOLoginCallback"):
        responses["302"] = {"description": "OAuth 授权或兼容回调的跳转", "headers": {"Location": {"schema": {"type": "string"}}}}
        if handler == "SSOLoginCallback":
            responses["200"]["content"]["text/html"] = {"schema": {"type": "string", "description": "非兼容模式使用 HTML postMessage 回传 token 或绑定凭据"}}
    elif handler == "WorkerSegmentUpload":
        responses["200"]["description"] = "业务失败的 JSON 信封；成功使用 HTTP 204"
        responses["204"] = {"description": "切片上传成功，无响应体"}


def export(inventory):
    schemas = Schemas(inventory["types"])
    contracts = {}
    paths = {}
    guide = (DOCS / "ai-guide.md").read_text()
    custom = (DOCS / "115-open-offline.md").read_text()
    source_files = set(inventory["source_files"]) | {"go.mod", "go.sum"}
    for r in inventory["routes"]:
        contracts.update(reachable(inventory["contracts"], r["handler"]))
    for c in contracts.values():
        source_files.add(c["source"]["file"])
    digest = hashlib.sha256()
    for filename in sorted(source_files):
        digest.update(filename.encode() + b"\0" + (ROOT / filename).read_bytes() + b"\0")
    source_hash = digest.hexdigest()
    spec = {
        "openapi": "3.1.0",
        "info": {"title": "OpenList custom/115 API", "version": "custom-115", "description": guide},
        "servers": [{"url": "http://localhost:5244", "description": "替换为实际 BASE_URL；包含部署的站点子路径"}],
        "paths": paths,
        "components": {"schemas": schemas.schemas, "securitySchemes": {
            "OpenListToken": {"type": "apiKey", "in": "header", "name": "Authorization", "description": "原始 OpenList JWT/API token，不加 Bearer 前缀"},
            "WorkerSecret": {"type": "apiKey", "in": "header", "name": "Authorization", "description": "转码共享 worker secret；原始 secret 或 Bearer <secret>"},
            "WorkerCallbackToken": {"type": "http", "scheme": "bearer", "description": "领取任务中该 job 的 callback_token；必须使用 Bearer 前缀"},
        }},
        "x-openlist-source-sha256": source_hash,
        "x-openlist-115-contract": custom,
        "x-openlist-contracts": contracts,
        "x-openlist-any-extra-methods": ["CONNECT", "TRACE"],
    }
    schemas.schemas["Envelope"] = {"type": "object", "required": ["code", "message", "data"], "properties": {"code": {"type": "integer", "description": "业务码；不是 HTTP 状态码"}, "message": {"type": "string"}, "data": {}}, "description": "通常 HTTP 200；data 的动态结构见实际合同和 x-openlist-contracts"}
    task_info = schemas.ref("server/handles.TaskInfo")
    schemas.ref("internal/offline_download/tool.Open115TaskInfo")
    schemas.ref("server/handles.taskDeleteResult")
    schemas.ref("server/handles.addOfflineDownloadResult")
    schemas.schemas[schema_name("internal/offline_download/tool.Open115TaskInfo")]["properties"]["submission_state"]["enum"] = ["pending", "submitting", "accepted", "failed", "unknown", "legacy"]
    schemas.schemas[schema_name("internal/offline_download/tool.Open115TaskInfo")]["properties"]["cloud_delete_state"]["enum"] = ["none", "deleting", "unknown", "deleted"]
    for field in ("submission_state", "cloud_delete_state"):
        schemas.schemas[schema_name("server/handles.TaskInfo")]["properties"][field].update(schemas.schemas[schema_name("internal/offline_download/tool.Open115TaskInfo")]["properties"][field])
    schemas.schemas[schema_name("server/handles.taskDeleteResult")]["properties"]["cloud_state"]["enum"] = ["none", "deleting", "unknown", "deleted"]
    schemas.schemas[schema_name("server/handles.TaskInfo")]["properties"]["state"].update(enum=list(range(10)), description="0 Pending, 1 Running, 2 Succeeded, 3 Canceling, 4 Canceled, 5 Errored, 6 Failing, 7 Failed, 8 WaitingRetry, 9 BeforeRetry")
    manual_response_types = {"FsListSplit": "server/handles.FsListResp", "FsGetSplit": "server/handles.FsGetResp", "FsArchiveMetaSplit": "server/handles.ArchiveMetaResp", "FsDirs": "[]server/handles.DirResp", "CurrentUser": "server/handles.UserResp", "WorkerRegister": "internal/transcode.RegisterResponse", "WorkerHeartbeat": "internal/transcode.HeartbeatResponse", "WorkerClaim": "internal/transcode.ClaimResponse", "MultipartInit": "server/handles.MultipartInitResp"}
    for r in inventory["routes"]:
        family = reachable(inventory["contracts"], r["handler"])
        primary = inventory["contracts"][r["handler"]]
        bindings = unique([b for c in family.values() for b in c["bindings"]])
        params = unique([p for c in family.values() for p in c["parameters"]])
        is_task = "/task/" in r["path"]
        action = r["path"].rsplit("/", 1)[-1]
        # Task factories share Go handlers but their public bodies differ by action.
        if is_task:
            bindings = []
            params = []
            if action in ("info", "cancel", "delete", "retry"):
                params.append({"in": "query", "name": "tid"})
            if action in ("delete", "delete_some", "clear_done", "clear_succeeded"):
                params.append({"in": "query", "name": "delete_cloud_record"})
        methods = ANY_METHODS if r["method"] == "Any" else [r["method"].lower()]
        for method in methods:
            if method not in HTTP_METHODS:
                continue  # OpenAPI has no CONNECT or TRACE path operation fields.
            op = {"summary": r["handler"].split(".")[-1], "operationId": method + "_" + re.sub(r"[^A-Za-z0-9]", "_", r["path"]), "tags": ["task" if is_task else r["path"].split("/")[2]], "x-openlist-contract": r["handler"], "x-openlist-middleware": r["middleware"], "x-source": {k: r["source"][k] for k in ("file", "line")}, "responses": {"200": {"description": "普通业务响应（成功和失败）；检查 JSON code。特殊非 JSON 响应见合同。", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Envelope"}}}}}}
            if r["method"] == "Any":
                op["x-gin-any"] = True
            if any("Auth" in mw for mw in r["middleware"]):
                op["security"] = [{"OpenListToken": []}]
                if not any("AuthAdmin" in mw or "AuthNotGuest" in mw for mw in r["middleware"]):
                    op["security"].append({})  # Guest access depends on configuration and path permissions.
            else:
                op["security"] = []
            handler = r["handler"].split(".")[-1]
            if handler in ("WorkerRegister", "WorkerHeartbeat", "WorkerClaim"):
                op["security"] = [{"WorkerSecret": []}]
            elif handler in ("WorkerSegmentUpload", "WorkerJobFinish"):
                op["security"] = [{"WorkerCallbackToken": []}]
            converted = []
            for p in params:
                if p["in"] in ("file", "form") or p["name"].lower() in ("authorization", "content-type", "content-length"):
                    continue
                parameter_schema = {"type": "string"}
                if p["name"] == "delete_cloud_record":
                    parameter_schema["enum"] = ["true", "false"]
                item = {"name": p["name"], "in": p["in"], "required": p["in"] == "path", "schema": parameter_schema}
                if is_task and p["name"] == "tid":
                    item.update(required=True, description="本地任务 ID；不是 info_hash")
                elif p["name"] == "delete_cloud_record":
                    item["description"] = "缺省/false 仅删除本地记录；true 校验并清理 115 Open 云下载记录，保留文件。只能传一次字面值 true 或 false。"
                if p.get("default"):
                    item["x-go-default-expression"] = p["default"]
                converted.append(item)
            for name in re.findall(r":([^/]+)", r["path"]):
                if not any(p["name"] == name and p["in"] == "path" for p in converted):
                    converted.append({"name": name, "in": "path", "required": True, "schema": {"type": "string"}})
            body_schemas = []
            for b in bindings:
                if method in ("get", "head") and b["method"] not in ("ShouldBindJSON", "BindJSON"):
                    properties, required = schemas.fields(b["type"], "form")
                    for name, s in properties.items():
                        converted.append({"name": name, "in": "query", "required": name in required, "schema": s})
                else:
                    body_schemas.append(schemas.ref(b["type"]))
            if is_task and action.endswith("_some"):
                body_schemas = [{"type": "array", "items": {"type": "string"}}]
            if converted:
                op["parameters"] = unique(converted)
            if body_schemas:
                body = body_schemas[0] if len(body_schemas) == 1 else {"anyOf": body_schemas}
                op["requestBody"] = {"required": True, "content": {"application/json": {"schema": body}}}
            form = {p["name"]: {"type": "string", **({"format": "binary"} if p["in"] == "file" else {})} for p in params if p["in"] in ("form", "file")}
            if form and method not in ("get", "head"):
                op.setdefault("requestBody", {"required": True, "content": {}})["content"]["multipart/form-data"] = {"schema": {"type": "object", "properties": form}}
            if r["path"] in ("/api/fs/put", "/api/fs/multipart/chunk", "/api/transcode/worker/segment"):
                op["requestBody"] = {"required": True, "content": {"application/octet-stream": {"schema": {"type": "string", "format": "binary"}}}}
            if handler == "ImportMediaDB":
                op.setdefault("requestBody", {"required": True, "content": {}})["content"]["application/json"] = {"schema": schemas.ref("server/handles.MediaExportData")}
                op["description"] = "导入 raw JSON，或 multipart 的 file（JSON/ZIP 导出文件）。"
            elif handler in ("FinishAuthnLogin", "FinishAuthnRegistration"):
                op["requestBody"] = {"required": True, "content": {"application/json": {"schema": {"type": "object", "description": "浏览器 WebAuthn credential 的 JSON 表示，由 go-webauthn 解析；配合 Session 头中的 base64 session"}}}}
            response_type = manual_response_types.get(r["handler"].split(".")[-1])
            if is_task and action in ("info", "done", "undone"):
                data_schema = task_info if action == "info" else {"type": "array", "items": task_info}
            elif response_type:
                data_schema = schemas.ref(response_type)
            else:
                data_schema = None
            if data_schema:
                typed_response(op, data_schema)
            if handler == "AddOfflineDownload":
                data = {"type": "object", "properties": {"tasks": {"type": ["array", "null"], "items": task_info}, "results": {"type": "array", "items": schemas.ref("server/handles.addOfflineDownloadResult"), "description": "仅请求 tool=115 Open 时返回；index 对应原输入，包含空白项"}}}
                typed_response(op, data)
                op["description"] = "异步创建任务。115 Open 部分创建成功为业务码 200；全空为 400；非空但全失败为 500；检查 data.results，保存成功 task_id，不能重发整个批次获取 info_hash。后续按原 ID 查询。"
            elif is_task and action in ("delete", "delete_some", "clear_done", "clear_succeeded"):
                cloud = schemas.ref("server/handles.taskDeleteResult") if action == "delete" else {"type": "object", "required": ["results"], "properties": {"results": {"type": "array", "items": schemas.ref("server/handles.taskDeleteResult")}}}
                local = {"type": "object", "additionalProperties": {"type": "string"}} if action == "delete_some" else {"type": "null"}
                typed_response(op, {"anyOf": [cloud, local]})
                op["description"] = "仅本地删除保留旧响应；delete_cloud_record=true 时单条返回删除结果对象，批量/clear 返回 results 数组。批量顶层业务码 200 仍可能全部失败，必须检查每项 code 和 local_deleted。仅 115 Open 离线下载支持云清理；其他任务逐项冲突，不降级本地删除。详见 x-openlist-115-contract。"
            elif is_task and action in ("cancel_some", "retry_some"):
                typed_response(op, {"type": "object", "additionalProperties": {"type": "string"}, "description": "任务ID到错误的失败映射；空对象表示无逐项错误"})
            special_responses(op, handler)
            paths.setdefault(re.sub(r":([^/]+)", r"{\1}", r["path"]), {})[method] = op

    # Include JSON definitions used by responses and local source contracts.
    for c in contracts.values():
        for b in c["bindings"]:
            schemas.ref(b["type"])
        for name in c["types"]:
            schemas.ref(name)
    markdown = [guide, "\n## 115 Open 定制接口完整合同\n", custom.replace("# 115 Open offline download API", "### 115 Open offline download API", 1), "\n## 当前源码全量接口清单\n", f"源码摘要：`{source_hash}`。共 {len(inventory['routes'])} 个方法／路径注册项（Gin Any 按一个注册项计），OpenAPI 展开为 {sum(len(v) for v in paths.values())} 个标准方法操作。\n", "`Any` 推荐按 POST JSON 调用；OpenAPI 展开其七种可表示的标准方法，额外 CONNECT／TRACE 仅在 Gin Any 中支持。下面的请求结构名称可在后文查找。所有条目均来自当前源码，不含官方文档里未在当前分支注册的路径。\n", "| 方法 | 路径 | 请求结构／处理函数 |\n| --- | --- | --- |"]
    for r in inventory["routes"]:
        c = contracts[r["handler"]]
        inputs = ", ".join(b["type"] for b in unique(c["bindings"])) or r["handler"]
        if "/task/" in r["path"]:
            action = r["path"].rsplit("/", 1)[-1]
            inputs = "JSON ID 字符串数组" if action.endswith("_some") else "tid 查询参数" if action in ("info", "cancel", "delete", "retry") else "无请求体"
        markdown.append(f"| {r['method']} | `{r['path']}` | `{inputs}` |")
    markdown.append("\n## 请求结构与任务返回结构\n\n以下为源码中的 Go 结构，JSON 字段名、form 字段名及 binding 标签均保留；`json:\"-\"` 不对外序列化。没有 `binding:\"required\"` 仅表示绑定器不作该必填检查，handler 仍可能检查内容和权限。字段类型 `interface{}`／`any` 由运行时及驱动决定。\n")
    for name in sorted(schemas.schemas):
        original = schemas.schemas[name].get("x-go-type")
        if original in inventory["types"]:
            d = inventory["types"][original]
            markdown.extend([f"\n### `{original}`\n", f"源码：`{d['source']['file']}:{d['source']['line']}`。\n", "```go\n" + d["source"]["code"] + "\n```\n"])
    markdown.append("\n## 各处理函数的参数、响应与实现依据\n\n多个方法／路径可复用同一处理函数。下面列出处理函数直接读取的参数和响应构造；其调用的本地辅助函数也已收录。`common.SuccessResp(c, X)` 中的 `X` 是 `data`；错误的附加数据见 `ErrorWithDataResp`。变量或函数表达式不是固定 JSON 示例。完整函数源码在 OpenAPI 顶层 `x-openlist-contracts` 中，读取该文件可继续追踪条件响应和辅助函数。\n")
    for name in sorted(contracts):
        c = contracts[name]
        markdown.extend([f"\n### `{name}`\n", f"源码：`{c['source']['file']}:{c['source']['line']}`。\n"])
        if c["bindings"]:
            markdown.append("请求绑定：" + "; ".join(f"`{b['method']}` → `{b['type']}`" for b in unique(c["bindings"])) + "。\n")
        if c["parameters"]:
            markdown.append("直接参数：" + "; ".join(f"{p['in']} `{p['name']}`" + (f"，默认表达式 `{p['default']}`" if p.get("default") else "") for p in unique(c["parameters"])) + "。\n")
        if c["responses"]:
            markdown.append("```go\n" + "\n".join(dict.fromkeys(c["responses"])) + "\n```\n")
        if c["calls"]:
            markdown.append("本地后续调用：" + ", ".join(f"`{call}`" for call in sorted(set(c["calls"])) if call in contracts) + "。\n")
    return "\n".join(markdown).rstrip() + "\n", json.dumps(spec, ensure_ascii=False, indent=2, sort_keys=True) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="report generated document drift without writing")
    args = parser.parse_args()
    result = subprocess.run(["go", "run", "scripts/api_inventory.go"], cwd=ROOT, check=True, stdout=subprocess.PIPE)
    inventory = json.loads(result.stdout)
    markdown, spec = export(inventory)
    outputs = {DOCS / "openlist-api.md": markdown, DOCS / "openlist.openapi.json": spec}
    changed = []
    for filename, content in outputs.items():
        if not filename.exists() or filename.read_text() != content:
            changed.append(filename.relative_to(ROOT).as_posix())
            if not args.check:
                filename.write_text(content)
    if args.check and changed:
        print("API 文档需要重新生成：" + ", ".join(changed), file=sys.stderr)
        return 1
    print(f"API 文档{'一致性检查通过' if args.check else '已生成'}：{len(inventory['routes'])} 个方法／路径注册项")
    return 0


if __name__ == "__main__":
    sys.exit(main())
