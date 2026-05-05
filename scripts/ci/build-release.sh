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
checksum_path="$DIST_DIR/${RELEASE_STEM}_SHA256SUMS.txt"

if [[ "$TARGET_OS" == "windows" ]]; then
	binary_path="$bundle_dir/${BIN_NAME}.exe"
	archive_path="$DIST_DIR/${RELEASE_STEM}.zip"
else
	binary_path="$bundle_dir/$BIN_NAME"
	archive_path="$DIST_DIR/${RELEASE_STEM}.tar.gz"
fi

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

chmod 0755 "$binary_path" 2>/dev/null || true
cp "$ROOT_DIR/README.md" "$bundle_dir/README.md"
cp "$ROOT_DIR/docs/LICENSE" "$bundle_dir/LICENSE"

if [[ "$TARGET_OS" == "windows" ]]; then
	if command -v 7z >/dev/null 2>&1; then
		( cd "$stage_dir" && 7z a -tzip -mx=9 "$archive_path" "$RELEASE_STEM" >/dev/null )
	else
		( cd "$stage_dir" && powershell -NoProfile -Command "Compress-Archive -Path '$RELEASE_STEM' -DestinationPath '$archive_path' -Force" )
	fi
else
	tar -C "$stage_dir" -czf "$archive_path" "$RELEASE_STEM"
fi

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
shopt -s nullglob
for artifact in \
	"$DIST_DIR/${RELEASE_STEM}".tar.gz \
	"$DIST_DIR/${RELEASE_STEM}".zip \
	"$DIST_DIR/${BIN_NAME}_${PACKAGE_VERSION}_linux_"*.deb \
	"$DIST_DIR/${BIN_NAME}_${PACKAGE_VERSION}_linux_"*.rpm; do
	if [[ -f "$artifact" ]]; then
		checksum_file "$artifact" >> "$checksum_path"
	fi
done
shopt -u nullglob
