#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-${VERSION:-dev}}"
OUTPUT_DIR="${OUTPUT_DIR:-${ROOT_DIR}/dist/release}"
COMMIT="${COMMIT:-}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

if [[ ! "${VERSION}" =~ ^(dev|v?[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?)$ ]]; then
  die "version must be dev or a semantic version such as v1.2.3"
fi

if [[ -z "${COMMIT}" ]]; then
  if command -v git >/dev/null 2>&1 && git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    COMMIT="$(git -C "${ROOT_DIR}" rev-parse HEAD)"
  else
    COMMIT="unknown"
  fi
fi

command -v go >/dev/null 2>&1 || die "go is required"
command -v npm >/dev/null 2>&1 || die "npm is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

mkdir -p "${OUTPUT_DIR}"
STAGE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/cloudmanager-release.XXXXXX")"
trap 'rm -rf "${STAGE_ROOT}"' EXIT

npm --prefix "${ROOT_DIR}/web" ci
npm --prefix "${ROOT_DIR}/web" run build

LDFLAGS="-s -w -X github.com/ikun/cloud-account-manager/internal/buildinfo.Version=${VERSION} -X github.com/ikun/cloud-account-manager/internal/buildinfo.Commit=${COMMIT} -X github.com/ikun/cloud-account-manager/internal/buildinfo.BuildTime=${BUILD_TIME}"
PACKAGE_VERSION="${VERSION#v}"
TARGETS=("linux/amd64" "linux/arm64")

for target in "${TARGETS[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  package_name="cloud-account-manager_${PACKAGE_VERSION}_${os}_${arch}"
  package_dir="${STAGE_ROOT}/${package_name}"

  mkdir -p "${package_dir}/web" "${package_dir}/deploy"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build \
    -trimpath -ldflags "${LDFLAGS}" \
    -o "${package_dir}/cloudmanager" "${ROOT_DIR}/cmd/cloudmanager"
  cp -R "${ROOT_DIR}/web/dist" "${package_dir}/web/dist"
  cp -R "${ROOT_DIR}/deploy/systemd" "${package_dir}/deploy/systemd"
  cp "${ROOT_DIR}/.env.example" "${package_dir}/.env.example"
  cp "${ROOT_DIR}/.env.production.example" "${package_dir}/.env.production.example"
  cp "${ROOT_DIR}/README.md" "${package_dir}/README.md"
  cp "${ROOT_DIR}/CHANGELOG.md" "${package_dir}/CHANGELOG.md"
  if [[ -f "${ROOT_DIR}/LICENSE" ]]; then
    cp "${ROOT_DIR}/LICENSE" "${package_dir}/LICENSE"
  fi

  tar -C "${STAGE_ROOT}" -czf "${OUTPUT_DIR}/${package_name}.tar.gz" "${package_name}"
done

(
  cd "${OUTPUT_DIR}"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum cloud-account-manager_"${PACKAGE_VERSION}"_*.tar.gz >SHA256SUMS
  else
    shasum -a 256 cloud-account-manager_"${PACKAGE_VERSION}"_*.tar.gz >SHA256SUMS
  fi
)

printf 'release artifacts written to %s\n' "${OUTPUT_DIR}"
