#!/usr/bin/env bash
# Cross-compiles cmd/aidoku-run for KOReader's supported targets and bundles
# it into aidoku.koplugin/bin/<bin_arch>/, where <bin_arch> is the name
# binArch() in main.lua resolves at runtime -- "armv6" for older (ARMv6)
# Kindles, "armv7" for newer Kindle/Kobo devices, "arm64" for aarch64
# devices/desktops, "x64" for x86-64 desktops.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
plugin_dir="$repo_root/koreader/aidoku.koplugin"

# bin_arch:GOARCH:GOARM
targets=(
    "armv6:arm:6"
    "armv7:arm:7"
    "arm64:arm64:"
    "x64:amd64:"
)

for target in "${targets[@]}"; do
    IFS=":" read -r bin_arch goarch goarm <<<"$target"
    out_dir="$plugin_dir/bin/$bin_arch"
    mkdir -p "$out_dir"

    out_bin="$out_dir/aidoku-run"
    echo "building aidoku-run for $bin_arch (GOARCH=$goarch${goarm:+ GOARM=$goarm})..."
    env CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
        go build -C "$repo_root" -trimpath -ldflags="-s -w" -o "$out_bin" "./cmd/aidoku-run"
    chmod +x "$out_bin"
    echo "  -> $out_bin ($(du -h "$out_bin" | cut -f1))"
done
