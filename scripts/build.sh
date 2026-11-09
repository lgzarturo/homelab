#!/usr/bin/env bash
# Compila homelab para Linux, macOS y Windows en dist/. Funciona en Linux, macOS y Git Bash (Windows).
# Uso: scripts/build.sh            (VERSION=1.0.0 scripts/build.sh para fijar la versión)
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
mkdir -p dist
for destino in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os="${destino%/*}"
  arch="${destino#*/}"
  salida="dist/homelab-$os-$arch"
  if [ "$os" = windows ]; then salida="$salida.exe"; fi
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$salida" .
  echo "✓ $salida"
done
