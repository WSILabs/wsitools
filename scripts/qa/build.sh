#!/usr/bin/env bash
#
# build.sh — compile the wsitools binary that the QA scripts use, separately
# from the (slow) matrix run. Run this only when the code changes; run-matrix.sh
# then reuses the binary without recompiling.
#
# Usage:
#   scripts/qa/build.sh [OUTPUT_BINARY]
#
# Default output: $OUT/_bin/wsitools  (OUT defaults to /tmp/wsitools-qa) — the
# location run-matrix.sh / check-*.sh look for automatically. Override by passing
# a path, or by exporting WSITOOLS=/path for the other scripts.
#
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${OUT:-/tmp/wsitools-qa}"
BIN="${1:-$OUT/_bin/wsitools}"

mkdir -p "$(dirname "$BIN")"
echo ">> building wsitools from $ROOT"
( cd "$ROOT" && go build -o "$BIN" ./cmd/wsitools ) || { echo "build failed"; exit 1; }
echo ">> built: $BIN ($("$BIN" version 2>/dev/null | head -1))"
echo ">> run-matrix.sh will pick this up automatically (or set WSITOOLS=$BIN)"
