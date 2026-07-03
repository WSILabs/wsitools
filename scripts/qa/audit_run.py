"""Run the audit matrix: execute each case, invoke the invariant checker on its
output, write findings.jsonl, and delete outputs as it goes (disk guard)."""
from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
from pathlib import Path

from audit_model import Case, Finding
import audit_cases
import audit_invariants as inv


def _wsi_json(binp: str, args: list[str]) -> dict | None:
    """Run a wsitools --json command; return parsed stdout JSON or None on failure."""
    try:
        p = subprocess.run([binp, *args], capture_output=True, text=True, timeout=1800)
    except Exception:
        return None
    if p.returncode != 0:
        return None
    try:
        return json.loads(p.stdout)
    except Exception:
        return None


def _dicom_target(path: str) -> str:
    """A DICOM output is a directory of .dcm; external readers open one instance."""
    p = Path(path)
    if p.is_dir():
        dcm = sorted(p.glob("*.dcm"))
        return str(dcm[0]) if dcm else path
    return path


# Open-error substrings the external readers emit for a container/codec they simply
# don't support — an expected limitation (N/A), not a wsitools defect.
_OS_NA = re.compile(r"unsupported tiff compression|compression support is not configured|"
                    r"not a file that openslide can recognize|unsupported|cannot read", re.I)
_BF_NA = re.compile(r"Unable to find TiffCompres|unsupported compression", re.I)


def _external_open(tool_argv: list[str], path: str, na_re) -> tuple[str, str]:
    """Run an external reader on path. Returns (result, detail) where result is
    'ok' | 'na' (reader can't support this container/codec) | 'fail'."""
    target = _dicom_target(path)
    try:
        p = subprocess.run([*tool_argv, target], capture_output=True, text=True, timeout=600)
    except Exception as e:
        return "na", f"skipped ({e})"
    if p.returncode == 0:
        return "ok", ""
    blob = (p.stdout + p.stderr)
    detail = blob.strip().splitlines()[-1] if blob.strip() else f"exit {p.returncode}"
    return ("na", detail) if na_re.search(blob) else ("fail", detail)


def _ifd_pyramid_dims(ifds: dict) -> list:
    """Per pyramid-level IFD (width,height) from `dump-ifds --json`. Pyramid levels
    have an integer level_index >= 0; associated IFDs do not."""
    dims = []
    for e in (ifds.get("ifds") or []):
        li = e.get("level_index")
        if isinstance(li, int) and li >= 0:
            dims.append((e.get("width"), e.get("height")))
    return dims


def _raw_subtags(binp: str, path: str) -> list:
    """Per pyramid-IFD YCbCrSubSampling TAG as a '4:x:y' string, parsed from
    `dump-ifds --raw` (the --json output does NOT include raw TIFF tags like 530)."""
    try:
        p = subprocess.run([binp, "dump-ifds", "--raw", path], capture_output=True, text=True, timeout=600)
    except Exception:
        return []
    subs = []
    cur = None
    started = False
    for line in p.stdout.splitlines():
        if re.match(r"IFD \d+ @", line):
            if started:
                subs.append(cur)
            cur, started = None, True
        m = re.search(r"YCbCrSubSampling\)\s+\S+\s+count=\d+\s+value=\[(\d+), (\d+)\]", line)
        if m:
            cur = _subtag_str([int(m.group(1)), int(m.group(2))])
    if started:
        subs.append(cur)
    return subs


def _subtag_str(yc) -> str | None:
    if not yc or len(yc) != 2:
        return None
    return {(1, 1): "4:4:4", (2, 2): "4:2:0", (2, 1): "4:2:2", (1, 2): "4:4:0"}.get(tuple(yc), f"{yc[0]}x{yc[1]}")


def _cleanup(path: str) -> None:
    p = Path(path)
    if p.is_dir():
        shutil.rmtree(p, ignore_errors=True)
    elif p.exists():
        p.unlink(missing_ok=True)
    sidecar = Path(str(p).rsplit(".", 1)[0] + "_files")
    if sidecar.is_dir():
        shutil.rmtree(sidecar, ignore_errors=True)


def run(fixtures: str, outdir: str, binp: str, big: bool) -> None:
    out_root = Path(outdir)
    cases_dir = out_root / "cases"
    cases_dir.mkdir(parents=True, exist_ok=True)
    findings_path = out_root / "findings.jsonl"
    findings_path.unlink(missing_ok=True)

    src_info_cache: dict[str, dict] = {}
    swap_group: dict[str, dict] = {}

    def emit(f: Finding) -> None:
        with open(findings_path, "a") as fh:
            fh.write(json.dumps(f.to_dict()) + "\n")

    cases = audit_cases.enumerate_cases(fixtures, big=big)
    print(f">> {len(cases)} cases; outdir={outdir}")

    for c in cases:
        real_out = c.output.replace("OUTDIR", str(cases_dir))
        argv = [a.replace("OUTDIR", str(cases_dir)) for a in c.cmd_argv]
        proc = subprocess.run([binp, *argv], capture_output=True, text=True)
        errored = proc.returncode != 0
        if errored != c.expect_error:
            tail = proc.stderr.strip().splitlines()[-1] if proc.stderr.strip() else ""
            emit(Finding(c.id, "openability", "expected-outcome", "unexpected-error",
                "error" if c.expect_error else "success",
                ("error: " + tail) if errored else "success", "wsitools " + " ".join(argv)))
        if errored or c.transform_type == "read":
            continue
        if not os.path.exists(real_out):
            continue

        if c.input not in src_info_cache:
            src_info_cache[c.input] = _wsi_json(binp, ["info", "--json", c.input]) or {}
        src_info = src_info_cache[c.input]
        out_info = _wsi_json(binp, ["info", "--json", real_out])
        if out_info is None:
            emit(Finding(c.id, "openability", "output-reopens", "conformance",
                "wsitools info opens the output", "info failed", "wsitools " + " ".join(argv)))
            _cleanup(real_out)
            continue
        out_ifds = _wsi_json(binp, ["dump-ifds", "--json", real_out]) or {}
        ifd_dims = _ifd_pyramid_dims(out_ifds)
        subtags = _raw_subtags(binp, real_out)

        for f in inv.check_geometry(c, src_info, out_info):
            emit(f)
        for f in inv.check_codec(c, src_info, out_info):
            emit(f)
        for f in inv.check_pyramid(c, out_info):
            emit(f)
        for f in inv.check_subsampling(c, out_info, subtags):
            emit(f)
        for f in inv.check_metadata_sanity(c, out_info):
            emit(f)
        for f in inv.check_metadata_consistency(c, out_info, ifd_dims):
            emit(f)

        if c.transform_type == "container-swap":
            swap_group.setdefault(c.input, {})[c.output_container] = out_info

        # External conformance oracle (readers that exist for this container).
        if c.output_container in ("svs", "tiff", "cog-wsi", "dicom"):
            res, detail = _external_open(["openslide-show-properties"], real_out, _OS_NA)
            if res == "fail":
                emit(Finding(c.id, "openability", "openslide-opens", "conformance",
                    "OpenSlide opens the output", detail, "wsitools " + " ".join(argv)))
        if c.output_container in ("tiff", "ome-tiff", "cog-wsi", "dicom"):
            res, detail = _external_open(["showinf", "-nopix", "-no-upgrade"], real_out, _BF_NA)
            if res == "fail":
                emit(Finding(c.id, "openability", "bioformats-opens", "conformance",
                    "Bio-Formats opens the output", detail, "wsitools " + " ".join(argv)))

        _cleanup(real_out)

    for src, per in swap_group.items():
        for f in inv.check_cross_container(Path(src).name, src_info_cache.get(src, {}), per, f"convert --to <c> {src}"):
            emit(f)
    print(f">> findings written to {findings_path}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--fixtures", required=True)
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--bin", required=True)
    ap.add_argument("--big", action="store_true")
    a = ap.parse_args()
    run(a.fixtures, a.outdir, a.bin, a.big)
