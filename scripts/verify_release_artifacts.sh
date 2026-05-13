#!/usr/bin/env bash
set -euo pipefail

DIST_DIR=${1:-dist}

if [[ ! -d "$DIST_DIR" ]]; then
  echo "dist directory not found: $DIST_DIR" >&2
  exit 1
fi

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command missing: $1" >&2
    exit 1
  fi
}

need_cmd tar
need_cmd unzip
need_cmd sha256sum
need_cmd dpkg-deb
need_cmd rpm
need_cmd grep
need_cmd git

shopt -s nullglob

linux_archives=("$DIST_DIR"/compair_*_linux_*.tar.gz)
darwin_archives=("$DIST_DIR"/compair_*_darwin_*.tar.gz)
windows_archives=("$DIST_DIR"/compair_*_windows_*.zip)
debs=("$DIST_DIR"/compair_*_linux_*.deb)
rpms=("$DIST_DIR"/compair_*_linux_*.rpm "$DIST_DIR"/compair-*.rpm)

require_files() {
  local label=$1
  local count=$2
  if [[ "$count" -eq 0 ]]; then
    echo "missing expected ${label} artifacts" >&2
    exit 1
  fi
}

require_files "linux archive" "${#linux_archives[@]}"
require_files "darwin archive" "${#darwin_archives[@]}"
require_files "windows archive" "${#windows_archives[@]}"
require_files "deb package" "${#debs[@]}"
require_files "rpm package" "${#rpms[@]}"

if [[ ! -f "$DIST_DIR/checksums.txt" ]]; then
  echo "missing checksums.txt in $DIST_DIR" >&2
  exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$host_os" in
  linux|darwin) ;;
  *) host_os="" ;;
esac

host_arch=$(uname -m)
case "$host_arch" in
  x86_64|amd64) host_arch=amd64 ;;
  arm64|aarch64) host_arch=arm64 ;;
  *) host_arch="" ;;
esac

select_host_archive() {
  local os=$1
  local arch=$2
  shift 2
  local archive
  if [[ -n "$arch" ]]; then
    for archive in "$@"; do
      if [[ "$(basename "$archive")" == *"_${os}_${arch}."* ]]; then
        printf '%s\n' "$archive"
        return 0
      fi
    done
  fi
  printf '%s\n' "$1"
}

smoke_test_host_binary() {
  local label=$1
  local bin=$2
  echo "Smoke testing $label binary"
  "$bin" version >/dev/null
  "$bin" demo --offline >/dev/null
}

echo "Verifying release checksums"
(
  cd "$DIST_DIR"
  sha256sum -c checksums.txt --ignore-missing
)

echo "Checking Linux archive structure"
linux_smoke_dir="$tmp_dir/linux"
mkdir -p "$linux_smoke_dir"
linux_archive="${linux_archives[0]}"
if [[ "$host_os" == "linux" ]]; then
  linux_archive=$(select_host_archive linux "$host_arch" "${linux_archives[@]}")
fi
tar -xzf "$linux_archive" -C "$linux_smoke_dir"
linux_bin=$(find "$linux_smoke_dir" -type f -name compair | head -n 1)
if [[ -z "$linux_bin" ]]; then
  echo "linux archive did not contain compair binary" >&2
  exit 1
fi
if [[ "$host_os" == "linux" ]]; then
  smoke_test_host_binary "Linux" "$linux_bin"
fi

echo "Checking Darwin archive structure"
darwin_smoke_dir="$tmp_dir/darwin"
mkdir -p "$darwin_smoke_dir"
darwin_archive="${darwin_archives[0]}"
if [[ "$host_os" == "darwin" ]]; then
  darwin_archive=$(select_host_archive darwin "$host_arch" "${darwin_archives[@]}")
fi
tar -xzf "$darwin_archive" -C "$darwin_smoke_dir"
darwin_bin=$(find "$darwin_smoke_dir" -type f -name compair | head -n 1)
if [[ -z "$darwin_bin" ]]; then
  echo "darwin archive did not contain compair binary" >&2
  exit 1
fi
if [[ "$host_os" == "darwin" ]]; then
  smoke_test_host_binary "Darwin" "$darwin_bin"
fi

echo "Checking Windows archive structure"
windows_smoke_dir="$tmp_dir/windows"
mkdir -p "$windows_smoke_dir"
unzip -q "${windows_archives[0]}" -d "$windows_smoke_dir"
windows_bin=$(find "$windows_smoke_dir" -type f -name 'compair.exe' | head -n 1)
if [[ -z "$windows_bin" ]]; then
  echo "windows archive did not contain compair.exe" >&2
  exit 1
fi

echo "Inspecting deb package metadata"
dpkg-deb -f "${debs[0]}" Package Version Architecture >/dev/null
if ! dpkg-deb -c "${debs[0]}" | grep '/usr/bin/compair$' >/dev/null; then
  echo "deb package missing /usr/bin/compair" >&2
  exit 1
fi

echo "Inspecting rpm package metadata"
rpm -qp --qf '%{NAME} %{VERSION} %{ARCH}\n' "${rpms[0]}" >/dev/null
if ! rpm -qlp "${rpms[0]}" | grep '^/usr/bin/compair$' >/dev/null; then
  echo "rpm package missing /usr/bin/compair" >&2
  exit 1
fi

echo "Release artifact verification passed."
