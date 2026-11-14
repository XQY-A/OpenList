#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
frontend_dir=""
output="bin/openlist"
docker_build=false
image="openlist:custom-115"
source_commit=""
source_dirty=""

usage() {
  cat <<'EOF'
Usage:
  bash build-custom-115.sh [--frontend-dir PATH] [--output PATH]
  bash build-custom-115.sh --docker [--image NAME]

Build the frontend commit pinned in build-config/custom-115-frontend.conf,
then embed it in a native OpenList binary. A local frontend checkout must be
clean and match the pin. Docker builds fetch the same pinned frontend.

Native prerequisites: Git, Go (go.mod toolchain), Node.js and pnpm.
Docker prerequisites: Git and Docker.

Container-only source metadata: --source-commit SHA --source-dirty true|false
EOF
}

fail() {
  echo "Error: $*" >&2
  exit 1
}

while (($#)); do
  case "$1" in
    --frontend-dir|--output|--image|--source-commit|--source-dirty)
      (($# >= 2)) || fail "$1 requires a value"
      case "$1" in
        --frontend-dir) frontend_dir="$2" ;;
        --output) output="$2" ;;
        --image) image="$2" ;;
        --source-commit) source_commit="$2" ;;
        --source-dirty) source_dirty="$2" ;;
      esac
      shift 2
      ;;
    --docker) docker_build=true; shift ;;
    --help|-h) usage; exit 0 ;;
    *) fail "unknown argument: $1" ;;
  esac
done

# This is a versioned build input, not a floating branch or release name.
source "$repo_dir/build-config/custom-115-frontend.conf"
[[ "$frontend_commit" =~ ^[0-9a-f]{40}$ ]] || fail "the frontend pin must be a full commit SHA"
[[ "$frontend_repository" == https://github.com/*/*.git ]] || fail "the frontend repository must be a GitHub HTTPS Git URL"

backend_has_git=false
if [[ -d "$repo_dir/.git" || -f "$repo_dir/.git" ]]; then
  backend_has_git=true
  backend_commit="$(git -C "$repo_dir" rev-parse HEAD)"
  backend_dirty=false
  if [[ -n "$(git -C "$repo_dir" status --porcelain --untracked-files=normal)" ]]; then
    backend_dirty=true
  fi
  [[ -z "$source_commit" || "$source_commit" == "$backend_commit" ]] || fail "backend source commit does not match the checkout"
  [[ -z "$source_dirty" || "$source_dirty" == "$backend_dirty" ]] || fail "backend source dirty flag does not match the checkout"
else
  backend_commit="$source_commit"
  backend_dirty="$source_dirty"
fi
[[ "$backend_commit" =~ ^[0-9a-f]{40}$ ]] || fail "backend Git metadata is missing; use the documented Docker wrapper"
[[ "$backend_dirty" == true || "$backend_dirty" == false ]] || fail "backend source dirty flag is missing or invalid"

check_backend_source() {
  if [[ "$backend_has_git" == true ]]; then
    [[ "$(git -C "$repo_dir" rev-parse HEAD)" == "$backend_commit" ]] || fail "backend commit changed during the build"
    local current_dirty=false
    if [[ -n "$(git -C "$repo_dir" status --porcelain --untracked-files=normal)" ]]; then
      current_dirty=true
    fi
    [[ "$current_dirty" == "$backend_dirty" ]] || fail "backend worktree changed during the build"
  fi
}

if [[ "$docker_build" == true ]]; then
  [[ -z "$frontend_dir" ]] || fail "--frontend-dir is only available for native builds"
  exec docker build --file "$repo_dir/Dockerfile.custom-115" \
    --build-arg "BACKEND_COMMIT=$backend_commit" \
    --build-arg "BACKEND_DIRTY=$backend_dirty" \
    --tag "$image" "$repo_dir"
fi

for tool in git go node pnpm; do
  command -v "$tool" >/dev/null 2>&1 || fail "required command not found: $tool"
done
export GIT_TERMINAL_PROMPT=0
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/openlist-custom-115.XXXXXX")"
dist_replaced=false
artifacts_publishing=false
build_succeeded=false
if [[ "$output" == /* ]]; then
  output_path="$output"
else
  output_path="$repo_dir/$output"
fi
cleanup() {
  local status=$?
  local cleanup_failed=false
  if [[ "$dist_replaced" == true && "$build_succeeded" != true ]]; then
    rm -rf -- "$repo_dir/public/dist" || cleanup_failed=true
    if [[ -d "$temp_dir/previous-dist" ]]; then
      mv "$temp_dir/previous-dist" "$repo_dir/public/dist" || cleanup_failed=true
    fi
  fi
  if [[ "$artifacts_publishing" == true && "$build_succeeded" != true ]]; then
    for suffix in "" .build-info .sha256; do
      rm -f -- "$output_path$suffix" || cleanup_failed=true
      if [[ -f "$temp_dir/previous-output$suffix" ]]; then
        mv "$temp_dir/previous-output$suffix" "$output_path$suffix" || cleanup_failed=true
      fi
    done
  fi
  if [[ "$cleanup_failed" == true ]]; then
    echo "Error: unable to restore all previous outputs; backups remain in $temp_dir" >&2
    return 1
  fi
  rm -rf -- "$temp_dir"
  return "$status"
}
trap cleanup EXIT

for suffix in "" .build-info .sha256; do
  [[ ! -L "$output_path$suffix" ]] || fail "output must not be a symlink: $output$suffix"
  [[ ! -e "$output_path$suffix" || -f "$output_path$suffix" ]] || fail "output must be a regular file: $output$suffix"
done

if [[ -z "$frontend_dir" ]]; then
  frontend_dir="$temp_dir/frontend"
  git init --quiet "$frontend_dir"
  git -C "$frontend_dir" remote add origin "$frontend_repository"
  git -c http.version=HTTP/1.1 -C "$frontend_dir" fetch --depth=1 origin "$frontend_commit"
  git -C "$frontend_dir" checkout --quiet --detach FETCH_HEAD
else
  frontend_dir="$(cd -- "$frontend_dir" && pwd)"
fi

check_frontend_source() {
  [[ "$(git -C "$frontend_dir" rev-parse --show-toplevel)" == "$frontend_dir" ]] || fail "--frontend-dir must be the checkout root"
  [[ "$(git -C "$frontend_dir" rev-parse HEAD)" == "$frontend_commit" ]] || fail "frontend checkout does not match the pinned commit"
  [[ -z "$(git -C "$frontend_dir" status --porcelain --untracked-files=normal)" ]] || fail "frontend checkout has uncommitted changes"
}
check_frontend_source
check_backend_source
echo "Building backend $backend_commit (dirty: $backend_dirty) with frontend $frontend_commit"
(
  cd -- "$frontend_dir"
  bash build.sh --dev --locked-i18n --no-compress
)
check_frontend_source
check_backend_source

frontend_dist="$frontend_dir/dist"
for file in index.html VERSION SOURCE_COMMIT SOURCE_DIRTY; do
  [[ -s "$frontend_dist/$file" ]] || fail "frontend output is missing $file"
done
[[ "$(cat "$frontend_dist/SOURCE_COMMIT")" == "$frontend_commit" ]] || fail "frontend output has the wrong source commit"
[[ "$(cat "$frontend_dist/SOURCE_DIRTY")" == false ]] || fail "refusing frontend output built from uncommitted changes"
web_version="$(cat "$frontend_dist/VERSION")"
[[ "$web_version" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+_-]*$ ]] || fail "invalid frontend version"

# Replace generated assets only after the source and output checks pass.
cp -R "$frontend_dist" "$temp_dir/dist"
if [[ -f "$repo_dir/public/dist/README.md" ]]; then
  cp "$repo_dir/public/dist/README.md" "$temp_dir/dist/README.md"
fi
if [[ -d "$repo_dir/public/dist" ]]; then
  mv "$repo_dir/public/dist" "$temp_dir/previous-dist"
fi
dist_replaced=true
mv "$temp_dir/dist" "$repo_dir/public/dist"

built_at="$(date -u +'%F %T %z')"
version="custom-115-${backend_commit:0:12}"
if [[ "$backend_dirty" == true ]]; then
  version="$version-dirty"
fi
package="github.com/OpenListTeam/OpenList/v4/internal/conf"
ldflags="-w -s -X '$package.BuiltAt=$built_at' -X '$package.GitAuthor=The OpenList Projects Contributors <noreply@oplist.org>' -X '$package.GitCommit=$backend_commit' -X '$package.Version=$version' -X '$package.WebVersion=$web_version'"
if [[ "${OPENLIST_STATIC:-0}" == 1 ]]; then
  ldflags="-linkmode external -extldflags '-static -fpic' $ldflags"
fi

cd -- "$repo_dir"
mkdir -p -- "$(dirname -- "$output_path")"
go build -trimpath -tags=jsoniter -ldflags="$ldflags" -o "$temp_dir/openlist" .
cat >"$temp_dir/openlist.build-info" <<EOF
backend_commit=$backend_commit
backend_dirty=$backend_dirty
frontend_repository=$frontend_repository
frontend_commit=$frontend_commit
frontend_version=$web_version
built_at=$built_at
EOF
if command -v sha256sum >/dev/null 2>&1; then
  checksum="$(sha256sum <"$temp_dir/openlist")"
else
  checksum="$(shasum -a 256 <"$temp_dir/openlist")"
fi
checksum="${checksum%% *}"
[[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || fail "invalid binary SHA256"
printf '%s  %s\n' "$checksum" "$output" >"$temp_dir/openlist.sha256"
check_backend_source

# Prepare all receipts and backups before replacing any delivered artifact.
for suffix in "" .build-info .sha256; do
  if [[ -f "$output_path$suffix" ]]; then
    cp -p "$output_path$suffix" "$temp_dir/previous-output$suffix"
  fi
done
artifacts_publishing=true
for suffix in "" .build-info .sha256; do
  mv "$temp_dir/openlist$suffix" "$output_path$suffix"
done
build_succeeded=true
echo "Built $output ($version, frontend $web_version)"
