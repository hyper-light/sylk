#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)

BIN_NAME=${BIN_NAME:-sylk}
DIST_DIR=${DIST_DIR:-"$ROOT_DIR/dist"}
GOCACHE=${GOCACHE:-"$ROOT_DIR/.cache/go-build"}
GO_TAGS=${GO_TAGS:-}
TARGET_OS=${TARGET_OS:?TARGET_OS is required}
TARGET_ARCH=${TARGET_ARCH:?TARGET_ARCH is required}
VERSION=${VERSION:?VERSION is required}

PACKAGE_VERSION=${VERSION#v}
RELEASE_STEM="${BIN_NAME}_${PACKAGE_VERSION}_${TARGET_OS}_${TARGET_ARCH}"

mkdir -p "$DIST_DIR" "$GOCACHE"

stage_dir=$(mktemp -d)
trap 'rm -rf "$stage_dir"' EXIT

bundle_dir="$stage_dir/$RELEASE_STEM"
binary_path="$bundle_dir/$BIN_NAME"
archive_path="$DIST_DIR/${RELEASE_STEM}.tar.gz"
checksum_path="$DIST_DIR/${RELEASE_STEM}_SHA256SUMS.txt"

mkdir -p "$bundle_dir"

build_cmd=(go build -trimpath -o "$binary_path")
if [[ -n "$GO_TAGS" ]]; then
	build_cmd+=(-tags "$GO_TAGS")
fi
build_cmd+=(.)

(
	cd "$ROOT_DIR"
	CGO_ENABLED=1 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" GOCACHE="$GOCACHE" "${build_cmd[@]}"
)

chmod 0755 "$binary_path"
cp "$ROOT_DIR/README.md" "$bundle_dir/README.md"
cp "$ROOT_DIR/docs/LICENSE" "$bundle_dir/LICENSE"

tar -C "$stage_dir" -czf "$archive_path" "$RELEASE_STEM"

if [[ "$TARGET_OS" == "linux" ]]; then
	bash "$ROOT_DIR/scripts/ci/package-linux.sh" "$bundle_dir" "$DIST_DIR" "$PACKAGE_VERSION" "$TARGET_ARCH" "$BIN_NAME"
fi

checksum_file() {
	local file=$1
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file"
	else
		shasum -a 256 "$file"
	fi
}

: > "$checksum_path"
for artifact in "$DIST_DIR"/"${RELEASE_STEM}".tar.gz "$DIST_DIR"/"${BIN_NAME}"_"${PACKAGE_VERSION}"_linux_*; do
	if [[ -f "$artifact" ]]; then
		checksum_file "$artifact" >> "$checksum_path"
	fi
done
