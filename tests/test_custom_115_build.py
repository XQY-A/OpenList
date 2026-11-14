"""Check frontend provenance before the personal build replaces embedded assets."""

import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest


BUILD_SCRIPT = Path(__file__).resolve().parents[1] / "build-custom-115.sh"


class Custom115BuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="openlist build test ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.frontend = self.root / "frontend"
        self.backend = self.root / "backend"
        self.commands = self.root / "commands"
        self.commands.mkdir()
        for repo in [self.frontend, self.backend]:
            repo.mkdir()
            self.git(repo, "init", "--quiet")
            self.git(repo, "config", "user.name", "Build Test")
            self.git(repo, "config", "user.email", "build-test@example.invalid")

        (self.frontend / ".gitignore").write_text("dist/\n")
        (self.frontend / "source.txt").write_text("committed frontend\n")
        (self.frontend / "build.sh").write_text("""#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == '--dev --locked-i18n --no-compress' ]]
mkdir -p dist
printf '<html>personal frontend</html>\\n' >dist/index.html
printf 'v4.2.9-test\\n' >dist/VERSION
git rev-parse HEAD >dist/SOURCE_COMMIT
printf '%s\\n' "${TEST_DIRTY_STAMP:-false}" >dist/SOURCE_DIRTY
if [[ -n "${TEST_WRONG_STAMP:-}" ]]; then
  printf '%s\\n' "$TEST_WRONG_STAMP" >dist/SOURCE_COMMIT
fi
if [[ "${TEST_CHANGE_SOURCE:-false}" == true ]]; then
  printf 'changed during build\\n' >>source.txt
fi
if [[ -n "${TEST_CHANGE_BACKEND:-}" ]]; then
  printf '// changed during frontend build\\n' >"$TEST_CHANGE_BACKEND"
fi
""")
        self.commit(self.frontend)
        self.frontend_sha = self.git(self.frontend, "rev-parse", "HEAD").strip()

        shutil.copy2(BUILD_SCRIPT, self.backend / BUILD_SCRIPT.name)
        (self.backend / "build-config").mkdir()
        self.write_pin(self.frontend_sha)
        (self.backend / ".gitignore").write_text("bin/\npublic/dist/*\n!public/dist/README.md\n")
        dist = self.backend / "public/dist"
        dist.mkdir(parents=True)
        (dist / "README.md").write_text("upstream embed placeholder\n")
        (dist / "existing.txt").write_text("do not replace before validation\n")
        self.commit(self.backend)
        for command in ["node", "pnpm"]:
            self.executable(command, "#!/bin/sh\nexit 0\n")
        self.executable("go", """#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == build ]]
if [[ "${TEST_GO_FAIL:-false}" == true ]]; then
  echo 'simulated compilation failure' >&2
  exit 1
fi
while (($#)); do
  if [[ "$1" == -o ]]; then
    printf 'test binary\\n' >"$2"
    if [[ -n "${TEST_CHANGE_BACKEND_DURING_GO:-}" ]]; then
      printf '// changed during go build\\n' >"$TEST_CHANGE_BACKEND_DURING_GO"
    fi
    exit 0
  fi
  shift
done
exit 1
""")
        self.env = os.environ.copy()
        self.env["PATH"] = str(self.commands) + os.pathsep + self.env["PATH"]

    def executable(self, name, content):
        path = self.commands / name
        path.write_text(content)
        path.chmod(0o755)

    def git(self, repo, *args):
        return subprocess.check_output(
            ["git", "-C", str(repo), *args], text=True, stderr=subprocess.PIPE
        )

    def commit(self, repo):
        self.git(repo, "add", "--all")
        self.git(repo, "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "test fixture")

    def write_pin(self, sha):
        (self.backend / "build-config/custom-115-frontend.conf").write_text(
            'frontend_repository="https://github.com/example/frontend.git"\n'
            f'frontend_commit="{sha}"\n'
        )

    def build(self, **env):
        return subprocess.run(
            ["bash", str(self.backend / BUILD_SCRIPT.name), "--frontend-dir", str(self.frontend)],
            cwd=self.backend,
            env={**self.env, **env},
            text=True,
            capture_output=True,
            timeout=20,
        )

    def assert_rejected_without_replacing_assets(self, result, message):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)
        self.assertTrue((self.backend / "public/dist/existing.txt").exists())
        self.assertFalse((self.backend / "bin/openlist").exists())

    def previous_outputs(self):
        output = self.backend / "bin/openlist"
        output.parent.mkdir()
        previous = {}
        for suffix in ["", ".build-info", ".sha256"]:
            path = Path(str(output) + suffix)
            previous[path] = f"previous verified output {suffix}\n"
            path.write_text(previous[path])
        return previous

    def assert_previous_outputs_preserved(self, previous):
        for path, contents in previous.items():
            self.assertEqual(path.read_text(), contents)
        self.assertTrue((self.backend / "public/dist/existing.txt").exists())

    def test_clean_pinned_checkout_is_embedded_with_provenance(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.backend / "public/dist/existing.txt").exists())
        self.assertEqual(
            (self.backend / "public/dist/README.md").read_text(),
            "upstream embed placeholder\n",
        )
        manifest = (self.backend / "bin/openlist.build-info").read_text()
        self.assertIn(f"frontend_commit={self.frontend_sha}\n", manifest)
        self.assertIn("backend_dirty=false\n", manifest)
        self.assertTrue((self.backend / "bin/openlist.sha256").is_file())

    def test_wrong_frontend_commit_is_rejected(self):
        self.write_pin("a" * 40)
        self.assert_rejected_without_replacing_assets(self.build(), "does not match the pinned commit")

    def test_dirty_frontend_is_rejected_before_build(self):
        (self.frontend / "source.txt").write_text("uncommitted change\n")
        self.assert_rejected_without_replacing_assets(self.build(), "has uncommitted changes")

    def test_wrong_output_commit_is_rejected(self):
        self.assert_rejected_without_replacing_assets(
            self.build(TEST_WRONG_STAMP="b" * 40), "output has the wrong source commit"
        )

    def test_dirty_output_is_rejected(self):
        self.assert_rejected_without_replacing_assets(
            self.build(TEST_DIRTY_STAMP="true"), "output built from uncommitted changes"
        )

    def test_build_cannot_change_source_and_claim_the_pin(self):
        self.assert_rejected_without_replacing_assets(
            self.build(TEST_CHANGE_SOURCE="true"), "has uncommitted changes"
        )

    def test_floating_frontend_pin_is_rejected(self):
        self.write_pin("custom/115")
        self.assert_rejected_without_replacing_assets(self.build(), "must be a full commit SHA")

    def test_backend_edit_during_frontend_build_is_rejected(self):
        result = self.build(TEST_CHANGE_BACKEND=str(self.backend / "new.go"))
        self.assert_rejected_without_replacing_assets(result, "backend worktree changed")

    def test_backend_edit_during_go_build_preserves_previous_binary(self):
        output = self.backend / "bin/openlist"
        output.parent.mkdir()
        output.write_text("previous verified binary\n")
        result = self.build(TEST_CHANGE_BACKEND_DURING_GO=str(self.backend / "new.go"))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("backend worktree changed", result.stderr)
        self.assertEqual(output.read_text(), "previous verified binary\n")
        self.assertTrue((self.backend / "public/dist/existing.txt").exists())

    def test_failed_go_build_preserves_previous_binary_and_assets(self):
        output = self.backend / "bin/openlist"
        output.parent.mkdir()
        output.write_text("previous verified binary\n")
        result = self.build(TEST_GO_FAIL="true")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated compilation failure", result.stderr)
        self.assertEqual(output.read_text(), "previous verified binary\n")
        self.assertTrue((self.backend / "public/dist/existing.txt").exists())
        self.assertEqual(
            (self.backend / "public/dist/README.md").read_text(),
            "upstream embed placeholder\n",
        )

    def test_dirty_backend_is_explicit_in_build_manifest(self):
        (self.backend / "local-change.go").write_text("// local preview\n")
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("backend_dirty=true\n", (self.backend / "bin/openlist.build-info").read_text())
        self.assertIn("-dirty, frontend", result.stdout)

    def test_incompatible_receipt_path_does_not_replace_outputs(self):
        previous = self.previous_outputs()
        receipt = self.backend / "bin/openlist.build-info"
        receipt.unlink()
        receipt.mkdir()
        result = self.build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("output must be a regular file", result.stderr)
        self.assertTrue(receipt.is_dir())
        previous.pop(receipt)
        self.assert_previous_outputs_preserved(previous)

    def test_checksum_failure_preserves_all_previous_outputs(self):
        previous = self.previous_outputs()
        self.executable("sha256sum", "#!/bin/sh\necho 'simulated checksum failure' >&2\nexit 1\n")
        result = self.build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated checksum failure", result.stderr)
        self.assert_previous_outputs_preserved(previous)

    def test_publication_failure_restores_all_previous_outputs(self):
        previous = self.previous_outputs()
        system_mv = shutil.which("mv")
        self.assertIsNotNone(system_mv)
        self.executable("mv", f"""#!/usr/bin/env bash
case "$1" in
  */openlist.sha256) echo 'simulated publication failure' >&2; exit 1 ;;
esac
exec {shlex.quote(system_mv)} "$@"
""")
        result = self.build()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("simulated publication failure", result.stderr)
        self.assert_previous_outputs_preserved(previous)


if __name__ == "__main__":
    unittest.main()
