#!/usr/bin/env bash
# Cross-compiles cmd/aidoku-run and cmd/aidoku-downloads for KOReader's
# supported targets and bundles each into aidoku.koplugin/bin/<jit.arch>/,
# where <jit.arch> is LuaJIT's arch name (see main.lua) -- "arm" for armv7
# Kindle/Kobo devices, "arm64" for aarch64 devices and desktop Linux.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
plugin_dir="$repo_root/koreader/aidoku.koplugin"

# jit_arch:GOARCH:GOARM
targets=(
    "arm:arm:7"
    "arm64:arm64:"
)
binaries=(
    "aidoku-run"
    "aidoku-downloads"
)

for target in "${targets[@]}"; do
    IFS=":" read -r jit_arch goarch goarm <<<"$target"
    out_dir="$plugin_dir/bin/$jit_arch"
    mkdir -p "$out_dir"

    for bin in "${binaries[@]}"; do
        out_bin="$out_dir/$bin"
        echo "building $bin for $jit_arch (GOARCH=$goarch${goarm:+ GOARM=$goarm})..."
        env CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
            go build -C "$repo_root" -trimpath -ldflags="-s -w" -o "$out_bin" "./cmd/$bin"
        chmod +x "$out_bin"
        echo "  -> $out_bin ($(du -h "$out_bin" | cut -f1))"
    done
done
