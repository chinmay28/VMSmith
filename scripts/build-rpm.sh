#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/bin"
PACKAGE_DIR="${BUILD_DIR}/packages"
RPMROOT="${BUILD_DIR}/rpmroot"
ARCH="${ARCH:-amd64}"
RPM_ARCH="${RPM_ARCH:-x86_64}"
BINARY_NAME="vmsmith-linux-${ARCH}"
BINARY_PATH="${BUILD_DIR}/${BINARY_NAME}"
VERSION="${VERSION:-${1:-}}"
RELEASE="${RELEASE:-1}"

if [[ -z "${VERSION}" ]]; then
  VERSION="$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

# RPM versions cannot contain '-' in Version.
VERSION="${VERSION#v}"
VERSION="${VERSION//-/_}"

if ! command -v rpmbuild >/dev/null 2>&1; then
  echo "error: rpmbuild is required (install rpm-build)." >&2
  exit 1
fi

if [[ ! -f "${BINARY_PATH}" ]]; then
  cat >&2 <<EOF
error: expected release binary at ${BINARY_PATH}
build it first with:
  make dist
EOF
  exit 1
fi

rm -rf "${RPMROOT}"
mkdir -p \
  "${PACKAGE_DIR}" \
  "${RPMROOT}/BUILD" \
  "${RPMROOT}/BUILDROOT" \
  "${RPMROOT}/RPMS" \
  "${RPMROOT}/SOURCES" \
  "${RPMROOT}/SPECS" \
  "${RPMROOT}/SRPMS"

PAYLOAD_ROOT="${RPMROOT}/payload"
mkdir -p \
  "${PAYLOAD_ROOT}/usr/local/bin" \
  "${PAYLOAD_ROOT}/usr/lib/systemd/system" \
  "${PAYLOAD_ROOT}/usr/share/licenses/vmsmith" \
  "${PAYLOAD_ROOT}/etc/vmsmith"

install -m 0755 "${BINARY_PATH}" "${PAYLOAD_ROOT}/usr/local/bin/vmsmith"
install -m 0644 "${ROOT_DIR}/vmsmith.service" "${PAYLOAD_ROOT}/usr/lib/systemd/system/vmsmith.service"
install -m 0644 "${ROOT_DIR}/vmsmith.yaml.example" "${PAYLOAD_ROOT}/etc/vmsmith/config.yaml"
install -m 0644 "${ROOT_DIR}/LICENSE" "${PAYLOAD_ROOT}/usr/share/licenses/vmsmith/LICENSE"

cat > "${RPMROOT}/SPECS/vmsmith.spec" <<EOF
Name:           vmsmith
Version:        ${VERSION}
Release:        ${RELEASE}%{?dist}
Summary:        VM Smith CLI, REST API, and web UI for QEMU/KVM hosts
License:        MIT
URL:            https://github.com/chinmay28/VMSmith
BuildArch:      ${RPM_ARCH}
Requires:       qemu-kvm
Requires:       libvirt
Requires:       cloud-init
Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd

%description
VMSmith is a single-binary CLI tool, REST API server, and embedded web GUI for
provisioning and managing QEMU/KVM virtual machines on Linux.

%install
mkdir -p %{buildroot}/usr/local/bin \
         %{buildroot}/usr/lib/systemd/system \
         %{buildroot}/usr/share/licenses/vmsmith \
         %{buildroot}/etc/vmsmith
cp -a ${PAYLOAD_ROOT}/usr/local/bin/vmsmith %{buildroot}/usr/local/bin/vmsmith
cp -a ${PAYLOAD_ROOT}/usr/lib/systemd/system/vmsmith.service %{buildroot}/usr/lib/systemd/system/vmsmith.service
cp -a ${PAYLOAD_ROOT}/usr/share/licenses/vmsmith/LICENSE %{buildroot}/usr/share/licenses/vmsmith/LICENSE
cp -a ${PAYLOAD_ROOT}/etc/vmsmith/config.yaml %{buildroot}/etc/vmsmith/config.yaml

%post
%systemd_post vmsmith.service

%preun
%systemd_preun vmsmith.service

%postun
%systemd_postun_with_restart vmsmith.service

%files
%license /usr/share/licenses/vmsmith/LICENSE
/usr/local/bin/vmsmith
/usr/lib/systemd/system/vmsmith.service
%config(noreplace) /etc/vmsmith/config.yaml

%changelog
* $(LC_ALL=C date '+%a %b %d %Y') March26 Bot <march26bot@gmail.com> - ${VERSION}-${RELEASE}
- Add RPM package build support for Rocky/RHEL/Fedora
EOF

rpmbuild \
  --define "_topdir ${RPMROOT}" \
  --define "_rpmdir ${PACKAGE_DIR}" \
  --define "_srcrpmdir ${PACKAGE_DIR}" \
  -bb "${RPMROOT}/SPECS/vmsmith.spec"

echo "RPM packages written to ${PACKAGE_DIR}"
find "${PACKAGE_DIR}" -type f \( -name '*.rpm' -o -name '*.src.rpm' \) -print | sort
