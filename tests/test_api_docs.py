"""Check source coverage and client-facing API contracts without running OpenList."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
module_spec = importlib.util.spec_from_file_location("export_api_docs", ROOT / "scripts/export_api_docs.py")
exporter = importlib.util.module_from_spec(module_spec)
module_spec.loader.exec_module(exporter)


class ApiDocumentationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="openlist-api-docs-")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.binary = Path(cls.temp.name) / "inventory"
        subprocess.run(["go", "build", "-o", str(cls.binary), "scripts/api_inventory.go"], cwd=ROOT, check=True)
        result = subprocess.run([str(cls.binary)], cwd=ROOT, check=True, stdout=subprocess.PIPE)
        cls.inventory = json.loads(result.stdout)
        cls.spec = json.loads((ROOT / "docs/api/openlist.openapi.json").read_text())

    def operation(self, path, method="post"):
        return self.spec["paths"][path][method]

    def test_generated_documents_match_current_source(self):
        markdown, spec = exporter.export(self.inventory)
        self.assertEqual(markdown, (ROOT / "docs/api/openlist-api.md").read_text())
        self.assertEqual(spec, (ROOT / "docs/api/openlist.openapi.json").read_text())

    def test_all_extracted_routes_and_any_methods_are_documented(self):
        expected = set()
        for route in self.inventory["routes"]:
            methods = exporter.HTTP_METHODS if route["method"] == "Any" else [route["method"].lower()]
            for method in methods:
                expected.add((route["path"], method))
        actual = {(path, method) for path, operations in self.spec["paths"].items() for method in operations}
        # Current /api paths have no colon parameters; normalize if that changes.
        import re
        expected = {(re.sub(r":([^/]+)", r"{\1}", path), method) for path, method in expected}
        self.assertEqual(expected, actual)
        self.assertEqual(self.spec["x-openlist-any-extra-methods"], ["CONNECT", "TRACE"])

    def test_task_families_and_legacy_admin_routes(self):
        types = ("upload", "copy", "move", "offline_download", "offline_download_transfer", "decompress", "decompress_upload", "seed_generate", "seed_save")
        for prefix in ("/api/task", "/api/admin/task"):
            for kind in types:
                base = f"{prefix}/{kind}"
                self.operation(base + "/done", "get")
                self.operation(base + "/undone", "get")
                for action in ("cancel_some", "delete_some", "retry_some"):
                    if kind == "seed_save" and action == "retry_some":
                        continue
                    body = self.operation(base + "/" + action)["requestBody"]["content"]["application/json"]["schema"]
                    self.assertEqual(body, {"type": "array", "items": {"type": "string"}})
                for action in ("retry", "retry_some", "retry_failed"):
                    self.assertEqual(base + "/" + action in self.spec["paths"], kind != "seed_save")

    def test_115_receipt_and_cloud_cleanup_contracts(self):
        add = self.operation("/api/fs/add_offline_download")
        data = add["responses"]["200"]["content"]["application/json"]["schema"]["properties"]["data"]["anyOf"][0]
        self.assertEqual(set(data["properties"]), {"tasks", "results"})
        self.assertIn("index", self.spec["components"]["schemas"]["server_handles.addOfflineDownloadResult"]["properties"])
        fields = self.spec["components"]["schemas"]["server_handles.TaskInfo"]["properties"]
        self.assertIn("unknown", fields["submission_state"]["enum"])
        self.assertIn("deleted", fields["cloud_delete_state"]["enum"])
        self.assertEqual(fields["source_file_delete_state"]["enum"], ["none", "deleting", "unknown", "deleted"])
        self.assertEqual(fields["can_delete_source_file"]["type"], "boolean")
        result_fields = self.spec["components"]["schemas"]["server_handles.taskDeleteResult"]["properties"]
        self.assertEqual(result_fields["source_file_delete_state"]["enum"], ["none", "deleting", "unknown", "deleted"])
        self.assertEqual(fields["state"]["enum"], list(range(10)))
        for action in ("delete", "delete_some", "clear_done", "clear_succeeded"):
            op = self.operation("/api/task/offline_download/" + action)
            for name in ("delete_cloud_record", "delete_source_file"):
                option = next(p for p in op["parameters"] if p["name"] == name)
                self.assertEqual(option["schema"]["enum"], ["true", "false"])
                self.assertEqual(option["schema"]["default"], "false")
            self.assertIn("delete_source_file=true", op["description"])
            data = op["responses"]["200"]["content"]["application/json"]["schema"]["properties"]["data"]["anyOf"][0]["anyOf"][0]
            self.assertIn("$ref" if action == "delete" else "properties", data)

    def test_worker_authentication_and_non_json_responses(self):
        for action in ("register", "heartbeat", "claim"):
            self.assertEqual(self.operation("/api/transcode/worker/" + action)["security"], [{"WorkerSecret": []}])
        segment = self.operation("/api/transcode/worker/segment", "put")
        self.assertEqual(segment["security"], [{"WorkerCallbackToken": []}])
        self.assertNotIn("content", segment["responses"]["204"])
        self.assertEqual(self.operation("/api/transcode/worker/job/finish")["security"], segment["security"])
        image = self.operation("/api/public/media/proxy", "get")
        self.assertIn("image/jpeg", image["responses"]["200"]["content"])
        self.assertNotIn("application/json", image["responses"]["200"]["content"])
        self.assertIn("502", image["responses"])
        for path, mime in (("/api/fs/media/strm", "text/plain"), ("/api/admin/media/export", "application/zip")):
            self.assertIn(mime, self.operation(path, "get")["responses"]["200"]["content"])

    def test_raw_and_third_party_request_bodies(self):
        content = self.operation("/api/admin/media/import")["requestBody"]["content"]
        self.assertEqual(set(content), {"application/json", "multipart/form-data"})
        self.assertIn("file", content["multipart/form-data"]["schema"]["properties"])
        for action in ("login", "registration"):
            self.assertIn("requestBody", self.operation("/api/authn/webauthn_finish_" + action))

    def test_custom_json_serialization_and_private_fields(self):
        schemas = self.spec["components"]["schemas"]
        self.assertEqual(set(schemas["internal_model.StorageDetails"]["properties"]), {"total_space", "used_space", "free_space"})
        self.assertNotIn("PwdTS", schemas["internal_model.User"]["properties"])
        task_fields = schemas["server_handles.TaskInfo"]["properties"]
        self.assertNotIn("account_id", task_fields)
        self.assertNotIn("storage_id", task_fields)

    def test_all_references_and_operation_identifiers_resolve(self):
        def visit(value):
            if isinstance(value, dict):
                if "$ref" in value:
                    target = self.spec
                    for part in value["$ref"].removeprefix("#/").split("/"):
                        target = target[part.replace("~1", "/").replace("~0", "~")]
                for item in value.values():
                    visit(item)
            elif isinstance(value, list):
                for item in value:
                    visit(item)
        visit(self.spec)
        ids = []
        for operations in self.spec["paths"].values():
            for op in operations.values():
                ids.append(op["operationId"])
                self.assertIn(op["x-openlist-contract"], self.spec["x-openlist-contracts"])
                params = [(p["in"], p["name"].lower() if p["in"] == "header" else p["name"]) for p in op.get("parameters", [])]
                self.assertEqual(len(params), len(set(params)))
                for container in [op.get("requestBody", {}), *op["responses"].values()]:
                    for media in container.get("content", {}).values():
                        # Media Type Object descriptions belong inside schema.
                        allowed = {"schema", "example", "examples", "encoding"}
                        self.assertFalse(set(media) - allowed - {key for key in media if key.startswith("x-")})
        self.assertEqual(len(ids), len(set(ids)))

    def test_inventory_fixture_groups_generics_and_binding_tags(self):
        result = self.fixture("""
g := e.Group(conf.URL.Path)
g.Use(middlewares.StoragesLoaded)
api := g.Group("/api", middlewares.Auth(false))
register(api.Group("/nested"))
handles.taskRoute[int](api.Group("/task/seed_save"), nil, false)
""", """
func register(g *gin.RouterGroup) { g.Any("/item", handles.Read) }
""")
        self.assertEqual(result.returncode, 0, result.stderr)
        inventory = json.loads(result.stdout)
        routes = {r["path"]: r for r in inventory["routes"]}
        self.assertEqual(set(routes), {"/api/nested/item", "/api/task/seed_save/done"})
        self.assertEqual(routes["/api/nested/item"]["method"], "Any")
        self.assertEqual(routes["/api/nested/item"]["middleware"], ["middlewares.StoragesLoaded", "middlewares.Auth(false)"])
        contract = inventory["contracts"]["server/handles.Read"]
        self.assertEqual(contract["bindings"][0]["type"], "server/handles.Request")
        self.assertIn("server/handles.helper", contract["calls"])
        self.assertIn("c.Status(204)", contract["responses"])
        builder = exporter.Schemas(inventory["types"])
        props, required = builder.fields("server/handles.Request")
        self.assertEqual(set(props), {"path", "names"})
        self.assertEqual(required, ["path"])
        self.assertIn("Path", builder.fields("server/handles.Request", "form")[0])

    def test_inventory_fails_for_unresolved_or_unsupported_registration(self):
        cases = (
            ('api.GET("/broken", handles.Unknown)', "unresolved handler"),
            ('missing(api)', "unresolved route helper"),
            ('for i := 0; i < 2; i++ { api.GET("/loop", handles.Read) }', "unsupported route registration construct"),
            ('api.GET("/repeat", handles.Read); api.GET("/repeat", handles.Read)', "duplicate route"),
        )
        for body, error in cases:
            with self.subTest(error=error):
                result = self.fixture('api := e.Group("/api"); ' + body)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(error, result.stderr)

    def fixture(self, body, helpers=""):
        with tempfile.TemporaryDirectory(prefix="openlist-api-fixture-") as folder:
            root = Path(folder)
            (root / "server/handles").mkdir(parents=True)
            (root / "server/router.go").write_text('''package server
import (
 "github.com/gin-gonic/gin"
 "github.com/OpenListTeam/OpenList/v4/internal/conf"
 "github.com/OpenListTeam/OpenList/v4/server/handles"
 "github.com/OpenListTeam/OpenList/v4/server/middlewares"
)
func Init(e *gin.Engine) { ''' + body + '\n}\n' + helpers)
            (root / "server/handles/handle.go").write_text('''package handles
import "github.com/gin-gonic/gin"
type Base struct { Path string `json:"path" binding:"required"` }
type Request struct { Base; Names []string `json:"names"`; Secret string `json:"-" form:"-"` }
func Read(c *gin.Context) { var req Request; c.ShouldBind(&req); helper[int](c); c.Status(204) }
func helper[T any](c *gin.Context) { c.Query("password") }
func taskRoute[T any](g *gin.RouterGroup, manager any, allowRetry bool) {
 canRetry := allowRetry
 g.GET("/done", Read)
 if canRetry { g.POST("/retry", Read) }
}
''')
            return subprocess.run([str(self.binary)], cwd=root, text=True, capture_output=True, timeout=20)


if __name__ == "__main__":
    unittest.main()
