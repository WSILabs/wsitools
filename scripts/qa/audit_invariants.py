"""Pure invariant rules over parsed wsitools --json output. Each check_* takes a
Case plus already-parsed info/dump-ifds dicts and returns a list[Finding]. No I/O."""
from __future__ import annotations

from typing import Any
from audit_model import Case, Finding


def _repro(case: Case) -> str:
    return "wsitools " + " ".join(case.cmd_argv)


def _l0(info: dict[str, Any]) -> dict[str, Any] | None:
    lv = info.get("levels") or []
    return lv[0] if lv else None


def check_geometry(case: Case, src_info: dict, out_info: dict) -> list[Finding]:
    findings: list[Finding] = []
    s, o = _l0(src_info), _l0(out_info)
    if not s or not o:
        return findings
    tt = case.transform_type
    if tt in ("container-swap", "recodec", "transcode", "roundtrip"):
        want_w, want_h = s["width"], s["height"]
        if (o["width"], o["height"]) != (want_w, want_h):
            findings.append(Finding(case.id, "geometry", "l0-dims-preserved",
                "silent-wrong-output", f"{want_w}x{want_h}", f'{o["width"]}x{o["height"]}', _repro(case)))
    elif tt in ("downsample", "factor"):
        n = case.factor if case.factor > 1 else 1
        want_w, want_h = s["width"] // n, s["height"] // n
        if abs(o["width"] - want_w) > 2 or abs(o["height"] - want_h) > 2:
            findings.append(Finding(case.id, "geometry", "l0-dims-scaled",
                "silent-wrong-output", f"≈{want_w}x{want_h} (src/{n})", f'{o["width"]}x{o["height"]}', _repro(case)))
    elif tt == "crop" and case.rect:
        _, _, rw, rh = (int(v) for v in case.rect.split(","))
        # Lossless snaps up to the tile grid (>= rect); lossy is ≈ rect.
        if case.lossless:
            if o["width"] < rw or o["height"] < rh:
                findings.append(Finding(case.id, "geometry", "crop-covers-rect",
                    "silent-wrong-output", f">={rw}x{rh}", f'{o["width"]}x{o["height"]}', _repro(case)))
        elif abs(o["width"] - rw) > 2 or abs(o["height"] - rh) > 2:
            findings.append(Finding(case.id, "geometry", "crop-dims",
                "silent-wrong-output", f"≈{rw}x{rh}", f'{o["width"]}x{o["height"]}', _repro(case)))
    return findings


def check_codec(case: Case, src_info: dict, out_info: dict) -> list[Finding]:
    findings: list[Finding] = []
    levels = out_info.get("levels") or []
    if not levels:
        return findings
    codecs = {lv["compression"] for lv in levels}
    if len(codecs) > 1:
        findings.append(Finding(case.id, "codec", "codec-uniform", "silent-wrong-output",
            "one codec across all levels", sorted(codecs), _repro(case)))
    l0codec = levels[0]["compression"]
    if case.requested_codec:
        if not _codec_matches(l0codec, case.requested_codec):
            findings.append(Finding(case.id, "codec", "output-codec-honored",
                "silent-wrong-output", case.requested_codec, l0codec, _repro(case)))
    return findings


def _codec_matches(info_codec: str, requested: str) -> bool:
    """info prints codec names like jpeg, jpeg2000, htj2k, avif, webp, jpeg-xl, png.
    Normalise both sides for comparison."""
    norm = lambda s: s.lower().replace("-", "").replace("_", "")
    return norm(info_codec) == norm(requested)
