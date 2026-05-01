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

shopt -s nullglob

linux_archives=("$DIST_DIR"/compair_*_linux_*.tar.gz)
darwin_archives=("$DIST_DIR"/compair_*_darwin_*.tar.gz)
windows_archives=("$DIST_DIR"/compair_*_windows_*.zip)
debs=("$DIST_DIR"/compair_*_linux_*.deb)
rpms=("$DIST_DIR"/compair-*.rpm)

require_files() {
  local label=$1
  shift
  if [[ $# -eq 0 ]]; then
    echo "missing expected ${label} artifacts" >&2
    exit 1
  fi
}

require_files "linux archive" "${linux_archives[@]}"
require_files "darwin archive" "${darwin_archives[@]}"
require_files "windows archive" "${windows_archives[@]}"
require_files "deb package" "${debs[@]}"
require_files "rpm package" "${rpms[@]}"

if [[ ! -f "$DIST_DIR/checksums.txt" ]]; then
  echo "missing checksums.txt in $DIST_DIR" >&2
  exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

echo "Verifying release checksums"
(
  cd "$DIST_DIR"
  sha256sum -c checksums.txt --ignore-missing
)

echo "Smoke testing Linux archive"
linux_smoke_dir="$tmp_dir/linux"
mkdir -p "$linux_smoke_dir"
tar -xzf "${linux_archives[0]}" -C "$linux_smoke_dir"
linux_bin=$(find "$linux_smoke_dir" -type f -name compair | head -n 1)
if [[ -z "$linux_bin" ]]; then
  echo "linux archive did not contain compair binary" >&2
  exit 1
fi
"$linux_bin" version >/dev/null

echo "Checking Darwin archive structure"
darwin_smoke_dir="$tmp_dir/darwin"
mkdir -p "$darwin_smoke_dir"
tar -xzf "${darwin_archives[0]}" -C "$darwin_smoke_dir"
darwin_bin=$(find "$darwin_smoke_dir" -type f -name compair | head -n 1)
if [[ -z "$darwin_bin" ]]; then
  echo "darwin archive did not contain compair binary" >&2
  exit 1
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
if ! dpkg-deb -c "${debs[0]}" | grep -q '/usr/bin/compair$'; then
  echo "deb package missing /usr/bin/compair" >&2
  exit 1
fi

echo "Inspecting rpm package metadata"
rpm -qp --qf '%{NAME} %{VERSION} %{ARCH}\n' "${rpms[0]}" >/dev/null
if ! rpm -qlp "${rpms[0]}" | grep -q '^/usr/bin/compair$'; then
  echo "rpm package missing /usr/bin/compair" >&2
  exit 1
fi

echo "Release artifact verification passed."
