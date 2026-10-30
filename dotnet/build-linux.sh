#!/usr/bin/env bash
# Build the Linux CLI (self-contained, trimmed single-file) into dist/linux/.
# Usage: dotnet/build-linux.sh
# Requires .NET SDK 8 (dotnet on PATH or at ~/.dotnet/dotnet).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DOTNET="$(command -v dotnet || true)"
[ -z "$DOTNET" ] && [ -x "$HOME/.dotnet/dotnet" ] && DOTNET="$HOME/.dotnet/dotnet"
if [ -z "$DOTNET" ]; then
    echo "[ERROR] dotnet SDK 8 not found. Install: curl -sSL https://dot.net/v1/dotnet-install.sh | bash -s -- --channel 8.0 --install-dir \"\$HOME/.dotnet\""
    exit 1
fi

"$DOTNET" publish "$ROOT_DIR/dotnet/LlmLimitsCli.Linux" -c Release -r linux-x64 \
    --self-contained true \
    -p:PublishSingleFile=true \
    -p:IncludeNativeLibrariesForSelfExtract=true \
    -p:PublishTrimmed=true \
    -o "$ROOT_DIR/dist/linux"

mv -f "$ROOT_DIR/dist/linux/llm-limits" "$ROOT_DIR/dist/linux/llm-limits-linux-x64"
chmod +x "$ROOT_DIR/dist/linux/llm-limits-linux-x64"

echo "[SUCCESS] Linux CLI built:"
ls -l "$ROOT_DIR/dist/linux/llm-limits-linux-x64"
