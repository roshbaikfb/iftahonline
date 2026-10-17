#!/usr/bin/env bash
# Installs pinned, checksum-verified binary tools into ./bin.
# Go-based tools are pinned in go.mod (tool directives) and run via `go tool`.
set -euo pipefail

BIN_DIR="${BIN_DIR:-$PWD/bin}"
mkdir -p "$BIN_DIR"

GOLANGCI_VERSION=2.15.0
GITLEAKS_VERSION=8.30.1
TRIVY_VERSION=0.76.0
SQLC_VERSION=1.31.1

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)  ARCH=amd64 ;;
  Linux-aarch64) ARCH=arm64 ;;
  *) echo "tools.sh: unsupported platform $(uname -s)-$(uname -m) (Linux amd64/arm64 only)" >&2; exit 1 ;;
esac

sha() {
  local name=$1
  case "$name-$ARCH" in
    golangci-lint-amd64) echo 9b7fe6a6bf6334b98decf8d86b11415ea7889afdabdd1959fdce23af2a9ebcea ;;
    golangci-lint-arm64) echo b4d8f17068b35ee6060cc2f69a16fb48740d706fbba81661c318649c114a6998 ;;
    gitleaks-amd64) echo 551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb ;;
    gitleaks-arm64) echo e4a487ee7ccd7d3a7f7ec08657610aa3606637dab924210b3aee62570fb4b080 ;;
    trivy-amd64)    echo e24b1c51293c1b15e4ba8d45bbc7891ba053f90278ec2698f3562c993e145fe7 ;;
    trivy-arm64)    echo aab21e677a7b7071fc79f1711480840754c18a1b9d9cefa7ccd06af63a0e8f4c ;;
    sqlc-amd64)     echo 497ae4fcdfa64c5b0c311ffe4c2bd991e43991e82e5367792ed78bc2dca27354 ;;
    sqlc-arm64)     echo b7cae247740d0c51a1e657479e5b2d21e6fef428f596682a01bc55bf4ab8a23d ;;
  esac
}

# install <name> <version> <url> <path-inside-tarball>
fetch() {
  local name=$1 version=$2 url=$3 member=$4
  local stamp="$BIN_DIR/.$name-$version"
  if [[ -x "$BIN_DIR/$name" && -f "$stamp" ]]; then return 0; fi
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  echo "tools.sh: installing $name $version"
  curl -fsSL --retry 3 -o "$tmp/archive.tgz" "$url"
  echo "$(sha "$name")  $tmp/archive.tgz" | sha256sum -c --quiet -
  tar -xzf "$tmp/archive.tgz" -C "$tmp" "$member"
  install -m 0755 "$tmp/$member" "$BIN_DIR/$name"
  rm -f "$BIN_DIR/.$name-"*
  touch "$stamp"
}

trivy_arch=$([[ $ARCH == amd64 ]] && echo 64bit || echo ARM64)
gitleaks_arch=$([[ $ARCH == amd64 ]] && echo x64 || echo arm64)

fetch golangci-lint "$GOLANGCI_VERSION" \
  "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}/golangci-lint-${GOLANGCI_VERSION}-linux-${ARCH}.tar.gz" \
  "golangci-lint-${GOLANGCI_VERSION}-linux-${ARCH}/golangci-lint"
fetch gitleaks "$GITLEAKS_VERSION" \
  "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${gitleaks_arch}.tar.gz" \
  gitleaks
fetch trivy "$TRIVY_VERSION" \
  "https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy_${TRIVY_VERSION}_Linux-${trivy_arch}.tar.gz" \
  trivy
fetch sqlc "$SQLC_VERSION" \
  "https://github.com/sqlc-dev/sqlc/releases/download/v${SQLC_VERSION}/sqlc_${SQLC_VERSION}_linux_${ARCH}.tar.gz" \
  sqlc
