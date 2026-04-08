#!/usr/bin/env bash

set -euo pipefail

BUNDLE_DIR=${1:?bundle dir is required}
DIST_DIR=${2:?dist dir is required}
PACKAGE_VERSION=${3:?package version is required}
TARGET_ARCH=${4:?target arch is required}
BIN_NAME=${5:-sylk}

case "$TARGET_ARCH" in
	amd64)
		DEB_ARCH=amd64
		RPM_ARCH=x86_64
		;;
	arm64)
		DEB_ARCH=arm64
		RPM_ARCH=aarch64
		;;
	*)
		echo "unsupported linux architecture: $TARGET_ARCH" >&2
		exit 1
		;;
esac

deb_path="$DIST_DIR/${BIN_NAME}_${PACKAGE_VERSION}_linux_${DEB_ARCH}.deb"
rpm_path="$DIST_DIR/${BIN_NAME}_${PACKAGE_VERSION}_linux_${RPM_ARCH}.rpm"

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

install_root="$work_dir/install-root"
mkdir -p \
	"$install_root/usr/bin" \
	"$install_root/usr/share/doc/$BIN_NAME" \
	"$install_root/DEBIAN"

cp "$BUNDLE_DIR/$BIN_NAME" "$install_root/usr/bin/$BIN_NAME"
cp "$BUNDLE_DIR/README.md" "$install_root/usr/share/doc/$BIN_NAME/README.md"
cp "$BUNDLE_DIR/LICENSE" "$install_root/usr/share/doc/$BIN_NAME/LICENSE"
chmod 0755 "$install_root/usr/bin/$BIN_NAME"

cat > "$install_root/DEBIAN/control" <<EOF
Package: $BIN_NAME
Version: $PACKAGE_VERSION
Section: utils
Priority: optional
Architecture: $DEB_ARCH
Maintainer: Sylk Maintainers <opensource@hyperlight.dev>
Recommends: bubblewrap, fuse3, git, gcc, g++
Description: Sylk terminal UI
 Sylk is a multi-agent terminal UI for coding, research, and project workflows.
 Dynamic tree-sitter grammar downloads use git plus a local C/C++ toolchain.
EOF

dpkg-deb --build --root-owner-group "$install_root" "$deb_path"

rpmbuild_root="$work_dir/rpmbuild"
source_root="$work_dir/${BIN_NAME}-${PACKAGE_VERSION}"
mkdir -p "$source_root/usr/bin" "$source_root/usr/share/doc/$BIN_NAME"
cp "$BUNDLE_DIR/$BIN_NAME" "$source_root/usr/bin/$BIN_NAME"
cp "$BUNDLE_DIR/README.md" "$source_root/usr/share/doc/$BIN_NAME/README.md"
cp "$BUNDLE_DIR/LICENSE" "$source_root/usr/share/doc/$BIN_NAME/LICENSE"

mkdir -p "$rpmbuild_root"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}
tar -C "$work_dir" -czf "$rpmbuild_root/SOURCES/${BIN_NAME}-${PACKAGE_VERSION}.tar.gz" "${BIN_NAME}-${PACKAGE_VERSION}"

cat > "$rpmbuild_root/SPECS/${BIN_NAME}.spec" <<EOF
Name: $BIN_NAME
Version: $PACKAGE_VERSION
Release: 1%{?dist}
Summary: Sylk terminal UI
License: MIT
URL: https://github.com/adalundhe/sylk
Source0: %{name}-%{version}.tar.gz
BuildArch: $RPM_ARCH
Recommends: bubblewrap, fuse3, git, gcc, gcc-c++

%description
Sylk is a multi-agent terminal UI for coding, research, and project workflows.
Dynamic tree-sitter grammar downloads use git plus a local C/C++ toolchain.

%prep
%setup -q

%build

%install
mkdir -p %{buildroot}/usr/bin %{buildroot}/usr/share/doc/$BIN_NAME
cp usr/bin/$BIN_NAME %{buildroot}/usr/bin/$BIN_NAME
cp usr/share/doc/$BIN_NAME/README.md %{buildroot}/usr/share/doc/$BIN_NAME/README.md
cp usr/share/doc/$BIN_NAME/LICENSE %{buildroot}/usr/share/doc/$BIN_NAME/LICENSE

%files
/usr/bin/$BIN_NAME
/usr/share/doc/$BIN_NAME/README.md
/usr/share/doc/$BIN_NAME/LICENSE
EOF

rpmbuild \
	--define "_topdir $rpmbuild_root" \
	--target "$RPM_ARCH" \
	-bb "$rpmbuild_root/SPECS/${BIN_NAME}.spec"

cp "$rpmbuild_root/RPMS/$RPM_ARCH"/*.rpm "$rpm_path"
