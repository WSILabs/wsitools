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


def check_pyramid(case: Case, out_info: dict) -> list[Finding]:
    findings: list[Finding] = []
    levels = out_info.get("levels") or []
    for i in range(1, len(levels)):
        if levels[i]["width"] >= levels[i - 1]["width"] or levels[i]["height"] >= levels[i - 1]["height"]:
            findings.append(Finding(case.id, "pyramid", "levels-monotonic", "silent-wrong-output",
                "strictly decreasing level dims",
                [(lv["width"], lv["height"]) for lv in levels], _repro(case)))
            break
    return findings


def check_subsampling(case: Case, out_info: dict, out_subtags: list) -> list[Finding]:
    findings: list[Finding] = []
    levels = out_info.get("levels") or []
    jpeg_levels = [(i, lv) for i, lv in enumerate(levels) if lv["compression"] == "jpeg"]
    for i, lv in jpeg_levels:
        bytes_sub = (lv.get("quality") or {}).get("chroma_subsampling")
        tag_sub = out_subtags[i] if i < len(out_subtags) else None
        if tag_sub is not None and bytes_sub is not None and tag_sub != bytes_sub:
            findings.append(Finding(case.id, "subsampling", "subsampling-tag-matches-bytes",
                "conformance", f"tag == bytes ({bytes_sub})", f"tag={tag_sub}, bytes={bytes_sub}", _repro(case)))
    subs = {(lv.get("quality") or {}).get("chroma_subsampling") for _, lv in jpeg_levels}
    subs.discard(None)
    if len(subs) > 1:
        findings.append(Finding(case.id, "subsampling", "subsampling-uniform", "silent-wrong-output",
            "one subsampling across the pyramid", sorted(subs), _repro(case)))
    return findings


def check_metadata_sanity(case: Case, out_info: dict) -> list[Finding]:
    findings: list[Finding] = []
    md = out_info.get("metadata") or {}
    mpp, mx, my = md.get("mpp"), md.get("mpp_x"), md.get("mpp_y")
    mag = md.get("magnification")

    def add(inv_id, exp, act):
        findings.append(Finding(case.id, "metadata-sanity", inv_id, "metadata-sanity", exp, act, _repro(case)))

    for name, v in (("mpp", mpp), ("mpp_x", mx), ("mpp_y", my)):
        if v is not None and v != 0 and v <= 0:
            add("mpp-positive", f"{name} > 0", v)
    if mpp is not None and mpp != 0 and mpp <= 0:
        add("mpp-positive", "mpp > 0", mpp)
    if mpp and mx and my and abs(mx - my) < 1e-9 and abs(mpp - mx) > 1e-6:
        add("mpp-axes-consistent", f"mpp == mpp_x == mpp_y ({mx})", mpp)
    if mx and my and mx > 0 and (max(mx, my) / min(mx, my)) > 1.5:
        add("mpp-axes-consistent", "mpp_x ≈ mpp_y (isotropic expected)", f"{mx} vs {my}")
    if mag is not None and mag != 0 and not (0.5 <= mag <= 160):
        add("magnification-plausible", "0.5 ≤ magnification ≤ 160", mag)
    for a in out_info.get("associated_images") or []:
        if a.get("width", 0) <= 0 or a.get("height", 0) <= 0:
            add("associated-dims-positive", f'{a.get("type")} > 0', f'{a.get("width")}x{a.get("height")}')
    for lv in out_info.get("levels") or []:
        if lv["width"] <= 0 or lv["height"] <= 0:
            add("level-dims-positive", "level dims > 0", f'{lv["width"]}x{lv["height"]}')
        if lv["tile_width"] <= 0 or lv["tile_height"] <= 0:
            add("tile-dims-positive", "tile dims > 0", f'{lv["tile_width"]}x{lv["tile_height"]}')
        q = (lv.get("quality") or {}).get("quality_estimate")
        if q is not None and not (0 <= q <= 100):
            add("quality-estimate-range", "0 ≤ quality_estimate ≤ 100", q)
    return findings


def check_metadata_consistency(case: Case, out_info: dict, out_ifd_dims: list) -> list[Finding]:
    findings: list[Finding] = []
    levels = out_info.get("levels") or []
    for i, lv in enumerate(levels):
        if i < len(out_ifd_dims):
            iw, ih = out_ifd_dims[i]
            if (lv["width"], lv["height"]) != (iw, ih):
                findings.append(Finding(case.id, "metadata-consistency", "info-matches-dumpifds-dims",
                    "metadata-inconsistency", f'info L{i} {lv["width"]}x{lv["height"]}',
                    f"dump-ifds {iw}x{ih}", _repro(case)))
    return findings
