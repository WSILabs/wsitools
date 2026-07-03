#!/usr/bin/env bash
# audit.sh — run the automated CLI matrix invariant audit and emit report.md.
# Usage: scripts/qa/audit.sh [--big] [--clean]
# Env:   WSITOOLS=/path/to/bin, SRC=/path/to/sample_files, OUT=/path/to/outdir
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="${SRC:-$ROOT/sample_files}"
OUT="${OUT:-/Volumes/Ext/tmp/wsitools-audit}"
BIN="${WSITOOLS:-$ROOT/bin/wsitools}"
BIG=""
for a in "$@"; do
  case "$a" in
    --big) BIG="--big" ;;
    --clean) rm -rf "$OUT" ;;
    *) echo "unknown flag: $a" >&2; exit 2 ;;
  esac
done
[[ -x "$BIN" ]] || { echo "no wsitools binary at $BIN (build: go build -o bin/wsitools ./cmd/wsitools)"; exit 1; }
export TMPDIR=/Volumes/Ext/tmp
mkdir -p "$OUT"
python3 "$ROOT/scripts/qa/audit_run.py" --fixtures "$SRC" --outdir "$OUT" --bin "$BIN" $BIG
python3 "$ROOT/scripts/qa/audit_report.py" --findings "$OUT/findings.jsonl" --out "$OUT/report.md"
echo ">> open $OUT/report.md"
