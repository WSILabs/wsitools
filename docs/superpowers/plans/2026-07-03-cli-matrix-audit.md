# CLI Matrix Invariant-Audit Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a rerunnable `scripts/qa/` harness that exercises a tiered matrix of wsitools CLI commands (inputs → outputs × options) and automatically catalogues discrepancies by checking invariants over `--json` output plus external-tool openability.

**Architecture:** Pure-Python (stdlib only) modules under `scripts/qa/`: dataclasses (`audit_model.py`) → invariant rules (`audit_invariants.py`, unit-tested) → matrix enumeration (`audit_cases.py`) → orchestrator that runs cases + checks + disk-guard cleanup (`audit_run.py`) → external oracle (reuse `check-openslide.sh`/`check-bioformats.sh`) → report (`audit_report.py`), wired by `audit.sh`. The invariant checker consumes `wsitools info --json` and `dump-ifds --json` (both already exist); `info.levels[].quality.chroma_subsampling` is the SOF-derived (bytes) subsampling and `dump-ifds` gives the `YCbCrSubSampling` tag, so tag≠bytes is a direct JSON compare.

**Tech Stack:** Python 3.12 (stdlib: `json`, `subprocess`, `dataclasses`, `pathlib`, `argparse`, `shutil`), `pytest` for unit tests, existing `scripts/qa/check-openslide.sh` / `check-bioformats.sh`, the `wsitools` binary.

**Spec:** `docs/superpowers/specs/2026-07-03-cli-matrix-audit-design.md`

**Conventions for the implementer:**
- Run everything under `TMPDIR=/Volumes/Ext/tmp` and write all harness output there — the main disk fills on large fixtures.
- The wsitools binary is at `./bin/wsitools` (build with `go build -o bin/wsitools ./cmd/wsitools` if missing).
- Fixtures live under `./sample_files/` (symlinked pool). Every `info`/`dump-ifds` invocation prints a benign `ld: warning: ignoring duplicate libraries` line to stderr — read stdout only for JSON.
- Commit after each task. Branch is `docs/cli-matrix-audit` (already created; spec committed there).

---

### Task 1: Data model — `Case` and `Finding`

**Files:**
- Create: `scripts/qa/audit_model.py`
- Test: `scripts/qa/tests/test_audit_model.py`

- [ ] **Step 1: Write the failing test**

```python
# scripts/qa/tests/test_audit_model.py
from audit_model import Case, Finding, SEVERITIES


def test_case_roundtrips_through_dict():
    c = Case(
        id="svs-to-tiff",
        cmd_argv=["convert", "--to", "tiff", "-o", "out.tiff", "in.svs"],
        input="in.svs",
        input_format="svs",
        output="out.tiff",
        output_container="tiff",
        transform_type="container-swap",
        requested_codec=None,
        factor=1,
        rect=None,
        lossless=False,
        expect_error=False,
        source_props={"mpp": 0.5},
    )
    d = c.to_dict()
    assert Case.from_dict(d) == c


def test_finding_severity_must_be_known():
    f = Finding(
        case_id="x", family="codec", invariant="output-codec-honored",
        severity="silent-wrong-output", expected="htj2k", actual="jpeg",
        repro="wsitools convert --to cog-wsi --codec htj2k -o o in",
    )
    assert f.severity in SEVERITIES
    assert f.to_dict()["family"] == "codec"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_model.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'audit_model'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/qa/audit_model.py
"""Shared vocabulary for the CLI matrix audit: Case (one command to run) and
Finding (one detected discrepancy)."""
from __future__ import annotations

import dataclasses
from dataclasses import dataclass, field
from typing import Any, Optional

# Severity ordering (worst first) — used to sort the report.
SEVERITIES = [
    "silent-wrong-output",      # succeeded but produced the wrong thing
    "conformance",              # opentile accepts it; OpenSlide/BF reject/misread
    "metadata-inconsistency",   # a field disagrees across commands/containers
    "metadata-sanity",          # a field's value is implausible
    "unexpected-error",         # errored when it should have worked (or vice-versa)
    "cosmetic",
]


@dataclass
class Case:
    id: str
    cmd_argv: list[str]          # wsitools args (no leading "wsitools")
    input: str
    input_format: str
    output: str
    output_container: str        # container/target of the output, or "read" for read-only cmds
    transform_type: str          # read|container-swap|downsample|factor|crop|transcode|recodec|roundtrip|associated-edit
    requested_codec: Optional[str]
    factor: int
    rect: Optional[str]          # "x,y,w,h" or None
    lossless: bool
    expect_error: bool
    source_props: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Case":
        return cls(**d)


@dataclass
class Finding:
    case_id: str
    family: str                  # geometry|pyramid|codec|subsampling|metadata-sanity|metadata-consistency|roundtrip|openability
    invariant: str               # short stable id of the rule
    severity: str
    expected: Any
    actual: Any
    repro: str

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_model.py -q`
Expected: PASS (2 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_model.py scripts/qa/tests/test_audit_model.py
git commit -m "qa(audit): Case/Finding data model"
```

---

### Task 2: Invariant rules — geometry + codec

**Files:**
- Create: `scripts/qa/audit_invariants.py`
- Test: `scripts/qa/tests/test_audit_invariants.py`

Context: `info --json` shape is
`{format, metadata:{make,model,software,datetime,mpp,mpp_x,mpp_y,magnification}, levels:[{index,width,height,tile_width,tile_height,compression,quality:{codec,lossless,quality_estimate,chroma_subsampling,notes}}], associated_images:[{type,width,height,compression}]}`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/qa/tests/test_audit_invariants.py
from audit_model import Case
import audit_invariants as inv


def _case(**kw):
    base = dict(id="c", cmd_argv=["convert"], input="in", input_format="svs",
                output="out", output_container="tiff", transform_type="container-swap",
                requested_codec=None, factor=1, rect=None, lossless=False,
                expect_error=False, source_props={})
    base.update(kw)
    return Case(**base)


def _info(levels, **md):
    return {"format": "tiff", "metadata": md, "levels": levels, "associated_images": []}


def _lvl(w, h, codec="jpeg", sub="4:4:4"):
    return {"index": 0, "width": w, "height": h, "tile_width": 256, "tile_height": 256,
            "compression": codec, "quality": {"codec": codec.upper(), "lossless": False,
            "quality_estimate": 85, "chroma_subsampling": sub, "notes": ""}}


def test_container_swap_preserves_l0_dims():
    src = _info([_lvl(2000, 3000)])
    ok = _info([_lvl(2000, 3000)])
    bad = _info([_lvl(1000, 3000)])
    assert inv.check_geometry(_case(), src, ok) == []
    f = inv.check_geometry(_case(), src, bad)
    assert len(f) == 1 and f[0].family == "geometry"


def test_factor_scales_l0_dims_within_tolerance():
    src = _info([_lvl(4000, 4000)])
    out = _info([_lvl(1000, 1000)])  # factor 4
    assert inv.check_geometry(_case(transform_type="factor", factor=4), src, out) == []
    bad = _info([_lvl(2000, 2000)])  # only halved
    assert len(inv.check_geometry(_case(transform_type="factor", factor=4), src, bad)) == 1


def test_codec_must_match_requested():
    src = _info([_lvl(100, 100, codec="jpeg")])
    out = _info([_lvl(100, 100, codec="jpeg")])
    c = _case(requested_codec="htj2k")
    f = inv.check_codec(c, src, out)
    assert len(f) == 1 and f[0].severity == "silent-wrong-output"


def test_codec_uniform_across_levels():
    src = _info([_lvl(100, 100)])
    out = {"format": "tiff", "metadata": {}, "associated_images": [],
           "levels": [_lvl(100, 100, codec="jpeg"), _lvl(50, 50, codec="jpeg2000")]}
    f = inv.check_codec(_case(), src, out)
    assert any(x.invariant == "codec-uniform" for x in f)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'audit_invariants'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/qa/audit_invariants.py
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
        want = case.requested_codec.replace("jpegxl", "jpeg-xl")  # info prints "jpeg-xl"
        if not _codec_matches(l0codec, case.requested_codec):
            findings.append(Finding(case.id, "codec", "output-codec-honored",
                "silent-wrong-output", case.requested_codec, l0codec, _repro(case)))
    return findings


def _codec_matches(info_codec: str, requested: str) -> bool:
    """info prints codec names like jpeg, jpeg2000, htj2k, avif, webp, jpeg-xl, png.
    Normalise both sides for comparison."""
    norm = lambda s: s.lower().replace("-", "").replace("_", "")
    return norm(info_codec) == norm(requested)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: PASS (4 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_invariants.py scripts/qa/tests/test_audit_invariants.py
git commit -m "qa(audit): geometry + codec invariants"
```

---

### Task 3: Invariant rules — pyramid + subsampling (tag vs bytes)

**Files:**
- Modify: `scripts/qa/audit_invariants.py`
- Test: `scripts/qa/tests/test_audit_invariants.py` (append)

Context: `info.levels[i].quality.chroma_subsampling` is the SOF-derived (bytes) value; `dump-ifds --json` gives the `YCbCrSubSampling` tag per IFD. The lossless-crop bug was tag=4:4:4 while bytes=4:2:0. We pass the per-level tag as a simple `list[str|None]` (`out_subtags[i]`) extracted by the orchestrator from dump-ifds, so this rule stays pure.

- [ ] **Step 1: Write the failing test (append)**

```python
def test_pyramid_dims_strictly_decreasing():
    out = {"format": "tiff", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000), _lvl(2000, 2000), _lvl(2000, 2000)]}
    f = inv.check_pyramid(_case(), out)
    assert any(x.invariant == "levels-monotonic" for x in f)


def test_subsampling_tag_must_match_bytes():
    out = {"format": "svs", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000, sub="4:4:4"), _lvl(2000, 2000, sub="4:2:0")]}
    # dump-ifds tags claim 4:4:4 on both (the bug shape).
    out_subtags = ["4:4:4", "4:4:4"]
    f = inv.check_subsampling(_case(), out, out_subtags)
    assert len(f) == 1 and f[0].invariant == "subsampling-tag-matches-bytes"
    assert f[0].severity == "conformance"


def test_subsampling_consistent_across_pyramid():
    out = {"format": "svs", "metadata": {}, "associated_images": [],
           "levels": [_lvl(4000, 4000, sub="4:4:4"), _lvl(2000, 2000, sub="4:2:0")]}
    out_subtags = ["4:4:4", "4:2:0"]  # tags match bytes, but pyramid is mixed
    f = inv.check_subsampling(_case(), out, out_subtags)
    assert any(x.invariant == "subsampling-uniform" for x in f)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: FAIL — `AttributeError: module 'audit_invariants' has no attribute 'check_pyramid'`

- [ ] **Step 3: Write minimal implementation (append to audit_invariants.py)**

```python
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
    # Only meaningful for JPEG (YCbCr) levels; RGB codecs carry no subsampling.
    jpeg_levels = [(i, lv) for i, lv in enumerate(levels) if lv["compression"] == "jpeg"]
    # (a) per-level tag (dump-ifds) must match the SOF-derived bytes value (info).
    for i, lv in jpeg_levels:
        bytes_sub = (lv.get("quality") or {}).get("chroma_subsampling")
        tag_sub = out_subtags[i] if i < len(out_subtags) else None
        if tag_sub is not None and bytes_sub is not None and tag_sub != bytes_sub:
            findings.append(Finding(case.id, "subsampling", "subsampling-tag-matches-bytes",
                "conformance", f"tag == bytes ({bytes_sub})", f"tag={tag_sub}, bytes={bytes_sub}", _repro(case)))
    # (b) subsampling should be uniform across the pyramid (the lossless-crop bug
    # produced a 4:4:4 base with 4:2:0 reduced levels).
    subs = {(lv.get("quality") or {}).get("chroma_subsampling") for _, lv in jpeg_levels}
    subs.discard(None)
    if len(subs) > 1:
        findings.append(Finding(case.id, "subsampling", "subsampling-uniform", "silent-wrong-output",
            "one subsampling across the pyramid", sorted(subs), _repro(case)))
    return findings
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: PASS (7 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_invariants.py scripts/qa/tests/test_audit_invariants.py
git commit -m "qa(audit): pyramid + subsampling (tag vs bytes) invariants"
```

---

### Task 4: Invariant rules — metadata sanity + cross-command consistency

**Files:**
- Modify: `scripts/qa/audit_invariants.py`
- Test: `scripts/qa/tests/test_audit_invariants.py` (append)

Context (explicit user emphasis): scrutinize every `info` metadata field for (a) sane values and (b) agreement with `dump-ifds`. `dump-ifds` per-IFD dims are passed as `out_ifd_dims: list[tuple[int,int]]`.

- [ ] **Step 1: Write the failing test (append)**

```python
def test_metadata_sanity_flags_nonpositive_mpp():
    out = _info([_lvl(100, 100)], mpp=0, mpp_x=0, mpp_y=0, magnification=20)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "mpp-positive" for x in f)


def test_metadata_sanity_flags_anisotropic_mpp_disagreeing_with_mpp():
    out = _info([_lvl(100, 100)], mpp=0.5, mpp_x=0.5, mpp_y=0.9, magnification=20)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "mpp-axes-consistent" for x in f)


def test_metadata_sanity_flags_implausible_magnification():
    out = _info([_lvl(100, 100)], mpp=0.5, mpp_x=0.5, mpp_y=0.5, magnification=9000)
    f = inv.check_metadata_sanity(_case(), out)
    assert any(x.invariant == "magnification-plausible" for x in f)


def test_metadata_consistency_info_vs_dumpifds_dims():
    out = _info([_lvl(2000, 3000)])
    f_ok = inv.check_metadata_consistency(_case(), out, [(2000, 3000)])
    assert f_ok == []
    f_bad = inv.check_metadata_consistency(_case(), out, [(2000, 9999)])
    assert any(x.invariant == "info-matches-dumpifds-dims" for x in f_bad)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: FAIL — `AttributeError: ... has no attribute 'check_metadata_sanity'`

- [ ] **Step 3: Write minimal implementation (append)**

```python
def check_metadata_sanity(case: Case, out_info: dict) -> list[Finding]:
    findings: list[Finding] = []
    md = out_info.get("metadata") or {}
    mpp, mx, my = md.get("mpp"), md.get("mpp_x"), md.get("mpp_y")
    mag = md.get("magnification")

    def add(inv_id, exp, act):
        findings.append(Finding(case.id, "metadata-sanity", inv_id, "metadata-sanity", exp, act, _repro(case)))

    # mpp fields present → must be positive.
    for name, v in (("mpp", mpp), ("mpp_x", mx), ("mpp_y", my)):
        if v is not None and v != 0 and v <= 0:
            add("mpp-positive", f"{name} > 0", v)
    if mpp and mpp <= 0:
        add("mpp-positive", "mpp > 0", mpp)
    # isotropic scalar mpp should equal the per-axis values when those are set/equal.
    if mpp and mx and my and abs(mx - my) < 1e-9 and abs(mpp - mx) > 1e-6:
        add("mpp-axes-consistent", f"mpp == mpp_x == mpp_y ({mx})", mpp)
    # anisotropic axes are allowed, but flag a large mismatch for human review.
    if mx and my and mx > 0 and (max(mx, my) / min(mx, my)) > 1.5:
        add("mpp-axes-consistent", "mpp_x ≈ mpp_y (isotropic expected)", f"{mx} vs {my}")
    # magnification in a sane WSI range.
    if mag is not None and mag != 0 and not (0.5 <= mag <= 160):
        add("magnification-plausible", "0.5 ≤ magnification ≤ 160", mag)
    # associated image dims must be positive.
    for a in out_info.get("associated_images") or []:
        if a.get("width", 0) <= 0 or a.get("height", 0) <= 0:
            add("associated-dims-positive", f'{a.get("type")} > 0', f'{a.get("width")}x{a.get("height")}')
    # every pyramid level: positive dims, positive tiles, quality estimate in range.
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
    # info's per-level dims must match dump-ifds' per-IFD dims (same underlying file).
    for i, lv in enumerate(levels):
        if i < len(out_ifd_dims):
            iw, ih = out_ifd_dims[i]
            if (lv["width"], lv["height"]) != (iw, ih):
                findings.append(Finding(case.id, "metadata-consistency", "info-matches-dumpifds-dims",
                    "metadata-inconsistency", f'info L{i} {lv["width"]}x{lv["height"]}',
                    f"dump-ifds {iw}x{ih}", _repro(case)))
    return findings
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: PASS (11 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_invariants.py scripts/qa/tests/test_audit_invariants.py
git commit -m "qa(audit): metadata sanity + cross-command consistency invariants"
```

---

### Task 5: Cross-container metadata consistency (group check)

**Files:**
- Modify: `scripts/qa/audit_invariants.py`
- Test: `scripts/qa/tests/test_audit_invariants.py` (append)

Context: one source fanned out to every container should report an identical metadata block + L0 dims. This is a *group* check over several outputs' info dicts (keyed by container).

- [ ] **Step 1: Write the failing test (append)**

```python
def test_cross_container_metadata_must_agree():
    src_id = "cmu2"
    per_container = {
        "svs": _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20),
        "tiff": _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20),
        "ome-tiff": _info([_lvl(2000, 3000)], make="Aperio", mpp=0.25, magnification=20),  # mpp drifted
    }
    f = inv.check_cross_container(src_id, per_container, "wsitools convert ...")
    assert any(x.invariant == "cross-container-metadata" and "mpp" in str(x.actual) for x in f)


def test_cross_container_agreement_is_clean():
    per = {c: _info([_lvl(2000, 3000)], make="Aperio", mpp=0.5, magnification=20) for c in ("svs", "tiff")}
    assert inv.check_cross_container("cmu2", per, "repro") == []
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: FAIL — `AttributeError: ... has no attribute 'check_cross_container'`

- [ ] **Step 3: Write minimal implementation (append)**

```python
# Metadata fields that MUST be container-independent for the same source.
_STABLE_MD = ["make", "model", "software", "datetime", "mpp", "mpp_x", "mpp_y", "magnification"]


def check_cross_container(src_id: str, per_container: dict, repro: str) -> list[Finding]:
    """per_container: {container_name: info_dict}. Flags any stable metadata field
    (or L0 dims) that is not identical across all containers for one source."""
    findings: list[Finding] = []
    containers = sorted(per_container)
    if len(containers) < 2:
        return findings
    ref_c = containers[0]
    ref_md = per_container[ref_c].get("metadata") or {}
    ref_l0 = (per_container[ref_c].get("levels") or [{}])[0]
    for c in containers[1:]:
        md = per_container[c].get("metadata") or {}
        for f in _STABLE_MD:
            if md.get(f) != ref_md.get(f):
                findings.append(Finding(f"{src_id}:{c}", "metadata-consistency", "cross-container-metadata",
                    "metadata-inconsistency", f"{f}={ref_md.get(f)} (as in {ref_c})",
                    f"{f}={md.get(f)} (in {c})", repro))
        l0 = (per_container[c].get("levels") or [{}])[0]
        if (l0.get("width"), l0.get("height")) != (ref_l0.get("width"), ref_l0.get("height")):
            findings.append(Finding(f"{src_id}:{c}", "metadata-consistency", "cross-container-l0-dims",
                "silent-wrong-output", f'{ref_l0.get("width")}x{ref_l0.get("height")} (in {ref_c})',
                f'{l0.get("width")}x{l0.get("height")} (in {c})', repro))
    return findings
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_invariants.py -q`
Expected: PASS (13 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_invariants.py scripts/qa/tests/test_audit_invariants.py
git commit -m "qa(audit): cross-container metadata consistency (group check)"
```

---

### Task 6: Matrix enumeration

**Files:**
- Create: `scripts/qa/audit_cases.py`
- Test: `scripts/qa/tests/test_audit_cases.py`

Context: build the tiered `list[Case]`. Only include a case if its input fixture exists on disk. Representative sources by role (resolve to whichever fixture is present):
- `svs_444` = `svs/CMU-2.svs`, `svs_420` = `svs/scan_620_.svs`, `svs_jp2k` = `svs/JP2K-33003-1.svs`, `svs_small` = `svs/CMU-1-Small-Region.svs`.
Valid output containers for the TIFF family fan-out: `svs, tiff, ome-tiff, cog-wsi, dzi, szi, dicom, bif, ife`.

- [ ] **Step 1: Write the failing test**

```python
# scripts/qa/tests/test_audit_cases.py
import os
import audit_cases


def test_enumerate_only_includes_existing_inputs(tmp_path):
    # Fake a fixtures dir with just one svs.
    (tmp_path / "svs").mkdir()
    (tmp_path / "svs" / "CMU-1-Small-Region.svs").write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    assert cases, "expected some cases for a present fixture"
    for c in cases:
        assert os.path.exists(c.input), f"case {c.id} references missing input {c.input}"


def test_container_swap_cases_have_expected_transform_type(tmp_path):
    (tmp_path / "svs").mkdir()
    (tmp_path / "svs" / "CMU-1-Small-Region.svs").write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    swaps = [c for c in cases if c.transform_type == "container-swap"]
    assert any(c.output_container == "tiff" for c in swaps)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_cases.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'audit_cases'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/qa/audit_cases.py
"""Enumerate the tiered representative audit matrix as a list[Case]. Only emits a
case when its input fixture exists on disk (missing fixtures are coverage gaps,
logged by the orchestrator, not errors)."""
from __future__ import annotations

import os
from audit_model import Case

# Output containers reachable via `convert --to` for the TIFF-family sources.
CONTAINERS = ["svs", "tiff", "ome-tiff", "cog-wsi", "dzi", "szi", "dicom", "bif", "ife"]
EXT = {"svs": "svs", "tiff": "tiff", "ome-tiff": "ome.tiff", "cog-wsi": "cog.tiff",
       "dzi": "dzi", "szi": "szi", "dicom": "dcmdir", "bif": "bif", "ife": "iris"}
# Codecs to sweep on the deep-transform sources (jpegxl needs --allow-nonconformant).
CODECS = ["jpeg", "jpeg2000", "htj2k", "avif", "webp", "jpegxl"]


def _fx(fixtures: str, rel: str) -> str | None:
    p = os.path.join(fixtures, rel)
    return p if os.path.exists(p) else None


def enumerate_cases(fixtures: str, big: bool) -> list[Case]:
    cases: list[Case] = []

    def add(cid, argv, inp, infmt, out, container, tt, **kw):
        cases.append(Case(id=cid, cmd_argv=argv, input=inp, input_format=infmt, output=out,
                          output_container=container, transform_type=tt,
                          requested_codec=kw.get("codec"), factor=kw.get("factor", 1),
                          rect=kw.get("rect"), lossless=kw.get("lossless", False),
                          expect_error=kw.get("expect_error", False), source_props={}))

    small = _fx(fixtures, "svs/CMU-1-Small-Region.svs")
    s444 = _fx(fixtures, "svs/CMU-2.svs")
    s420 = _fx(fixtures, "svs/scan_620_.svs")
    sjp2k = _fx(fixtures, "svs/JP2K-33003-1.svs")

    # --- T1: broad-shallow read commands over every present input format ---
    read_inputs = {
        "svs": small, "generic-tiff": _fx(fixtures, "generic-tiff/CMU-1.tiff"),
        "ome-tiff": _fx(fixtures, "ome-tiff/CMU-1.ome.tiff"), "cog-wsi": _fx(fixtures, "cog-wsi/CMU-1_cog-wsi.tiff"),
        "dicom": _fx(fixtures, "dicom/Leica-4"), "ndpi": _fx(fixtures, "ndpi/CMU-1.ndpi") if big else None,
        "bif": _fx(fixtures, "bif/OS-2.bif"), "ife": _fx(fixtures, "ife/425248_JPEG.iris") if big else None,
    }
    for fmt, path in read_inputs.items():
        if not path:
            continue
        add(f"info-{fmt}", ["info", "--json", path], path, fmt, path, "read", "read")
        add(f"dump-{fmt}", ["dump-ifds", "--json", path], path, fmt, path, "read", "read")
        add(f"validate-{fmt}", ["validate", "--json", path], path, fmt, path, "read", "read")
        add(f"hash-{fmt}", ["hash", "--mode", "pixel", path], path, fmt, path, "read", "read")

    # --- T1: container-swap — small SVS -> every container (defaults) ---
    if small:
        for c in CONTAINERS:
            out = f"OUTDIR/swap_{c}.{EXT[c]}"
            add(f"swap-svs-{c}", ["convert", "--to", c, "-f", "-o", out, small],
                small, "svs", out, c, "container-swap")

    # --- T2: deep transforms on the representative multi-level sources ---
    for role, src in (("444", s444), ("420", s420), ("jp2k", sjp2k)):
        if not src:
            continue
        # codec sweep -> tiff
        for codec in CODECS:
            out = f"OUTDIR/codec_{role}_{codec}.tiff"
            argv = ["convert", "--to", "tiff", "--codec", codec]
            if codec == "jpegxl":
                argv.append("--allow-nonconformant")
            argv += ["-f", "-o", out, src]
            add(f"codec-{role}-{codec}", argv, src, "svs", out, "tiff", "recodec", codec=codec)
        # factor sweep -> svs
        for n in (2, 4, 8):
            out = f"OUTDIR/factor_{role}_{n}.svs"
            add(f"factor-{role}-{n}", ["convert", "--to", "svs", "--factor", str(n), "-f", "-o", out, src],
                src, "svs", out, "factor", factor=n)
            outd = f"OUTDIR/down_{role}_{n}.svs"
            add(f"down-{role}-{n}", ["downsample", "--factor", str(n), "-f", "-o", outd, src],
                src, "svs", outd, "downsample", factor=n)
        # crop lossy + lossless
        rect = "4000,4000,8192,8192"
        add(f"crop-{role}", ["crop", "--x", "4000", "--y", "4000", "--w", "8192", "--h", "8192",
            "-f", "-o", f"OUTDIR/crop_{role}.svs", src], src, "svs", f"OUTDIR/crop_{role}.svs", "crop", rect=rect)
        add(f"cropll-{role}", ["crop", "--lossless", "--x", "4000", "--y", "4000", "--w", "8192", "--h", "8192",
            "-f", "-o", f"OUTDIR/cropll_{role}.svs", src], src, "svs", f"OUTDIR/cropll_{role}.svs",
            "crop", rect=rect, lossless=True)
        # tile-size re-tile
        add(f"tilesize-{role}", ["convert", "--to", "tiff", "--codec", "jpeg", "--tile-size", "512",
            "-f", "-o", f"OUTDIR/tile_{role}.tiff", src], src, "svs", f"OUTDIR/tile_{role}.tiff", "recodec", codec="jpeg")

    # --- T3: cross-format -> svs ---
    for fmt, path in (("bif", read_inputs["bif"]), ("ome-tiff", read_inputs["ome-tiff"]),
                      ("cog-wsi", read_inputs["cog-wsi"]), ("dicom", read_inputs["dicom"])):
        if path:
            out = f"OUTDIR/x_{fmt}_to_svs.svs"
            add(f"x-{fmt}-svs", ["convert", "--to", "svs", "-f", "-o", out, path],
                path, fmt, out, "svs", "container-swap")

    return cases
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_cases.py -q`
Expected: PASS (2 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_cases.py scripts/qa/tests/test_audit_cases.py
git commit -m "qa(audit): tiered matrix enumeration"
```

---

### Task 7: Orchestrator — run cases, check, disk-guard cleanup

**Files:**
- Create: `scripts/qa/audit_run.py`
- Test: manual smoke (Step 4)

Context: for each Case, substitute `OUTDIR` with the real output dir, run the wsitools command, and — on a produced output — pull `info --json` (source once, output) + `dump-ifds --json` (output) and run every invariant, appending to `findings.jsonl`. Then delete the output (disk guard) keeping only findings/logs. Group container-swap outputs of the same source for the cross-container check.

- [ ] **Step 1: Write the module**

```python
# scripts/qa/audit_run.py
"""Run the audit matrix: execute each case, invoke the invariant checker on its
output, write findings.jsonl, and delete outputs as it goes (disk guard)."""
from __future__ import annotations

import argparse
import json
import os
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


def _ifd_pyramid_dims(ifds: dict) -> list:
    """Per pyramid-level IFD (width,height) from `dump-ifds --json`. Confirmed
    schema: {path, format, ifds:[{index, image_type, level_index, width, height,
    tile_width, tile_height, compression_tag, compression, subfile_type}]}.
    Pyramid levels have an integer level_index >= 0; associated IFDs do not."""
    dims = []
    for e in (ifds.get("ifds") or []):
        li = e.get("level_index")
        if isinstance(li, int) and li >= 0:
            dims.append((e.get("width"), e.get("height")))
    return dims


import re as _re


def _raw_subtags(binp: str, path: str) -> list:
    """Per pyramid-IFD YCbCrSubSampling TAG as a '4:x:y' string, parsed from
    `dump-ifds --raw` (the --json output does NOT include raw TIFF tags like 530).
    Walks IFDs in order; associated-image IFDs (no 530 tag) yield None and are
    filtered by position against the pyramid dims from _ifd_pyramid_dims."""
    try:
        p = subprocess.run([binp, "dump-ifds", "--raw", path], capture_output=True, text=True, timeout=600)
    except Exception:
        return []
    subs = []
    cur = None  # current IFD's tag (default None until a 530 line seen)
    started = False
    for line in p.stdout.splitlines():
        if _re.match(r"IFD \d+ @", line):
            if started:
                subs.append(cur)
            cur, started = None, True
        m = _re.search(r"YCbCrSubSampling\)\s+\S+\s+count=\d+\s+value=\[(\d+), (\d+)\]", line)
        if m:
            cur = _subtag_str([int(m.group(1)), int(m.group(2))])
    if started:
        subs.append(cur)
    return subs


def _subtag_str(yc) -> str | None:
    if not yc or len(yc) != 2:
        return None
    return {(1, 1): "4:4:4", (2, 2): "4:2:0", (2, 1): "4:2:2", (1, 2): "4:4:0"}.get(tuple(yc), f"{yc[0]}x{yc[1]}")


def run(fixtures: str, outdir: str, binp: str, big: bool) -> None:
    out_root = Path(outdir)
    cases_dir = out_root / "cases"
    cases_dir.mkdir(parents=True, exist_ok=True)
    findings_path = out_root / "findings.jsonl"
    findings_path.unlink(missing_ok=True)

    src_info_cache: dict[str, dict] = {}
    swap_group: dict[str, dict] = {}  # src -> {container: info}

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
            emit(Finding(c.id, "openability", "expected-outcome", "unexpected-error",
                "error" if c.expect_error else "success",
                "error: " + proc.stderr.strip().splitlines()[-1] if errored else "success",
                "wsitools " + " ".join(argv)))
        if errored or c.transform_type == "read":
            continue
        if not os.path.exists(real_out):
            continue

        # Source info (cached), output info + dump-ifds + validate.
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

        _cleanup(real_out)

    # Cross-container consistency over each source's container fan-out.
    for src, per in swap_group.items():
        for f in inv.check_cross_container(Path(src).name, per, f"convert --to <c> {src}"):
            emit(f)
    print(f">> findings written to {findings_path}")


def _cleanup(path: str) -> None:
    p = Path(path)
    if p.is_dir():
        shutil.rmtree(p, ignore_errors=True)
    elif p.exists():
        p.unlink(missing_ok=True)
    # DZI/SZI leave a sidecar _files dir; remove it too.
    sidecar = Path(str(p).rsplit(".", 1)[0] + "_files")
    if sidecar.is_dir():
        shutil.rmtree(sidecar, ignore_errors=True)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--fixtures", required=True)
    ap.add_argument("--outdir", required=True)
    ap.add_argument("--bin", required=True)
    ap.add_argument("--big", action="store_true")
    a = ap.parse_args()
    run(a.fixtures, a.outdir, a.bin, a.big)
```

- [ ] **Step 2: Sanity-confirm the two dump-ifds shapes used above**

Run: `bin/wsitools dump-ifds --json sample_files/svs/CMU-1-Small-Region.svs 2>/dev/null | python3 -c "import sys,json;d=json.load(sys.stdin);print((d['ifds'][0]).keys())"`
Expected: includes `level_index, width, height` (confirmed schema — `_ifd_pyramid_dims` filters on `level_index >= 0`).
Run: `bin/wsitools dump-ifds --raw sample_files/svs/CMU-1-Small-Region.svs 2>/dev/null | grep -c YCbCrSubSampling`
Expected: ≥1 (the raw dump carries tag 530, which `_raw_subtags` parses; the `--json` output does not).

- [ ] **Step 3: Smoke-run on the small SVS only**

Run:
```bash
TMPDIR=/Volumes/Ext/tmp python3 scripts/qa/audit_run.py \
  --fixtures "$(pwd)/sample_files" --outdir /Volumes/Ext/tmp/wsitools-audit \
  --bin "$(pwd)/bin/wsitools"
```
Expected: prints `>> N cases`, then `>> findings written to …/findings.jsonl`; `cat /Volumes/Ext/tmp/wsitools-audit/findings.jsonl` shows zero or a few JSON findings; the `cases/` dir is left small (outputs deleted).

- [ ] **Step 4: Commit**

```bash
git add scripts/qa/audit_run.py
git commit -m "qa(audit): orchestrator with per-case checks + disk-guard cleanup"
```

---

### Task 8: External oracle — OpenSlide / Bio-Formats openability

**Files:**
- Modify: `scripts/qa/audit_run.py` (call the external validators before cleanup)
- Depends on: existing `scripts/qa/check-openslide.sh`, `scripts/qa/check-bioformats.sh`

- [ ] **Step 1: Confirm the validators' interface**

Run: `sed -n '1,40p' scripts/qa/check-openslide.sh; echo ---; sed -n '1,40p' scripts/qa/check-bioformats.sh`
Expected: read how each takes a path and signals pass/fail (exit code / stdout). Note the exact invocation.

- [ ] **Step 2: Add an external-oracle helper to audit_run.py**

Insert after `_wsi_json` (adjust the invocation to match Step 1's findings — the validators exist and take a path; wire their real flags):

```python
def _external_openable(script: str, path: str) -> tuple[bool, str]:
    """Run an external validator script on path. Returns (ok, detail)."""
    try:
        p = subprocess.run(["bash", script, path], capture_output=True, text=True, timeout=600)
    except Exception as e:
        return True, f"skipped ({e})"  # never let the oracle crash the run
    return p.returncode == 0, (p.stdout + p.stderr).strip().splitlines()[-1] if (p.stdout or p.stderr) else ""
```

- [ ] **Step 3: Call the oracle for TIFF-family / DICOM outputs before cleanup**

In `run()`, immediately before `_cleanup(real_out)`, add:

```python
        # External conformance oracle for readers that exist for this container.
        qa_dir = str(Path(__file__).parent)
        if c.output_container in ("svs", "tiff", "ome-tiff", "cog-wsi"):
            ok, detail = _external_openable(os.path.join(qa_dir, "check-openslide.sh"), real_out)
            if not ok:
                emit(Finding(c.id, "openability", "openslide-opens", "conformance",
                    "OpenSlide opens the output", detail, "wsitools " + " ".join(argv)))
        if c.output_container in ("ome-tiff", "tiff", "dicom"):
            ok, detail = _external_openable(os.path.join(qa_dir, "check-bioformats.sh"), real_out)
            if not ok:
                emit(Finding(c.id, "openability", "bioformats-opens", "conformance",
                    "Bio-Formats opens the output", detail, "wsitools " + " ".join(argv)))
```

- [ ] **Step 4: Smoke-run and confirm no oracle crashes**

Run:
```bash
TMPDIR=/Volumes/Ext/tmp python3 scripts/qa/audit_run.py \
  --fixtures "$(pwd)/sample_files" --outdir /Volumes/Ext/tmp/wsitools-audit \
  --bin "$(pwd)/bin/wsitools"
```
Expected: completes; any OpenSlide/BF failures appear as `openability` findings, not tracebacks.

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_run.py
git commit -m "qa(audit): external OpenSlide/Bio-Formats openability oracle"
```

---

### Task 9: Report generator

**Files:**
- Create: `scripts/qa/audit_report.py`
- Test: `scripts/qa/tests/test_audit_report.py`

- [ ] **Step 1: Write the failing test**

```python
# scripts/qa/tests/test_audit_report.py
import json
import audit_report


def test_report_groups_by_severity_and_dedups(tmp_path):
    findings = [
        {"case_id": "a", "family": "codec", "invariant": "output-codec-honored",
         "severity": "silent-wrong-output", "expected": "htj2k", "actual": "jpeg", "repro": "r1"},
        {"case_id": "b", "family": "codec", "invariant": "output-codec-honored",
         "severity": "silent-wrong-output", "expected": "avif", "actual": "jpeg", "repro": "r2"},
        {"case_id": "c", "family": "metadata-sanity", "invariant": "mpp-positive",
         "severity": "metadata-sanity", "expected": "mpp>0", "actual": 0, "repro": "r3"},
    ]
    fp = tmp_path / "findings.jsonl"
    fp.write_text("\n".join(json.dumps(f) for f in findings) + "\n")
    md = audit_report.render(str(fp))
    # Worst severity section comes first.
    assert md.index("silent-wrong-output") < md.index("metadata-sanity")
    # Both codec findings are grouped under one invariant heading with 2 instances.
    assert "output-codec-honored" in md and "2 case" in md
    assert "r1" in md and "r3" in md
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_report.py -q`
Expected: FAIL — `ModuleNotFoundError: No module named 'audit_report'`

- [ ] **Step 3: Write minimal implementation**

```python
# scripts/qa/audit_report.py
"""Aggregate findings.jsonl into a human-triage report.md: grouped by severity,
deduped by (family, invariant), each with instance count + repro commands."""
from __future__ import annotations

import argparse
import json
from collections import defaultdict

from audit_model import SEVERITIES


def render(findings_path: str) -> str:
    findings = []
    with open(findings_path) as fh:
        for line in fh:
            line = line.strip()
            if line:
                findings.append(json.loads(line))

    by_sev: dict[str, dict] = defaultdict(lambda: defaultdict(list))
    for f in findings:
        by_sev[f["severity"]][(f["family"], f["invariant"])].append(f)

    out = ["# wsitools CLI matrix audit — findings", "",
           f"Total findings: {len(findings)}", ""]
    for sev in SEVERITIES:
        groups = by_sev.get(sev)
        if not groups:
            continue
        out.append(f"## {sev}")
        out.append("")
        for (family, invariant), items in sorted(groups.items()):
            out.append(f"### `{invariant}` ({family}) — {len(items)} case(s)")
            for it in items[:8]:  # cap examples; note if truncated
                out.append(f"- **{it['case_id']}**: expected `{it['expected']}`, got `{it['actual']}`")
                out.append(f"  - repro: `{it['repro']}`")
            if len(items) > 8:
                out.append(f"- …and {len(items) - 8} more")
            out.append("")
    if len(out) <= 4:
        out.append("_No discrepancies found._")
    return "\n".join(out)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--findings", required=True)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    with open(a.out, "w") as fh:
        fh.write(render(a.findings))
    print(f">> report written to {a.out}")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_report.py -q`
Expected: PASS (1 passed)

- [ ] **Step 5: Commit**

```bash
git add scripts/qa/audit_report.py scripts/qa/tests/test_audit_report.py
git commit -m "qa(audit): severity-grouped report generator"
```

---

### Task 10: `audit.sh` entry point + full smoke run + self-check

**Files:**
- Create: `scripts/qa/audit.sh`
- Modify: `scripts/qa/MANUAL-TEST-PLAN.md` (document the new automated audit)

- [ ] **Step 1: Write the entry script**

```bash
# scripts/qa/audit.sh
#!/usr/bin/env bash
# audit.sh — run the automated CLI matrix invariant audit and emit report.md.
# Usage: scripts/qa/audit.sh [--big] [--clean]
# Env:   WSITOOLS=/path/to/bin, SRC=/path/to/sample_files, OUT=/path/to/outdir
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SRC="${SRC:-$ROOT/sample_files}"
OUT="${OUT:-/Volumes/Ext/tmp/wsitools-audit}"
BIN="${WSITOOLS:-$ROOT/bin/wsitools}"
BIG=""; for a in "$@"; do case "$a" in --big) BIG="--big";; --clean) rm -rf "$OUT";; esac; done
[[ -x "$BIN" ]] || { echo "no wsitools binary at $BIN (build: go build -o bin/wsitools ./cmd/wsitools)"; exit 1; }
export TMPDIR=/Volumes/Ext/tmp
mkdir -p "$OUT"
python3 "$ROOT/scripts/qa/audit_run.py" --fixtures "$SRC" --outdir "$OUT" --bin "$BIN" $BIG
python3 "$ROOT/scripts/qa/audit_report.py" --findings "$OUT/findings.jsonl" --out "$OUT/report.md"
echo ">> open $OUT/report.md"
```

- [ ] **Step 2: Make executable and run the unit suite**

Run:
```bash
chmod +x scripts/qa/audit.sh
cd scripts/qa && python3 -m pytest tests/ -q
```
Expected: all unit tests pass.

- [ ] **Step 3: Full smoke run**

Run: `scripts/qa/audit.sh --clean`
Expected: completes without traceback; prints `>> open …/report.md`; `report.md` exists and is well-formed markdown; disk under `/Volumes/Ext/tmp/wsitools-audit/cases` stays small (outputs deleted as it goes).

- [ ] **Step 4: Self-check — the harness has teeth**

Run: `grep -iE "subsampling|codec|geometry|metadata|cross-container" /Volumes/Ext/tmp/wsitools-audit/report.md | head`
Expected: the report contains real invariant sections. Sanity-confirm at least one *known* property holds: since the cog-wsi/chroma bugs are FIXED on main, those specific findings should be ABSENT (a clean pass), while any genuinely new discrepancy is surfaced. If the report is suspiciously empty, temporarily point `--bin` at a pre-fix commit's binary to confirm the harness *would* have flagged the cog-wsi/chroma bugs (documents that the checks fire).

- [ ] **Step 5: Document + commit**

Append a short section to `scripts/qa/MANUAL-TEST-PLAN.md`:

```markdown
## Automated invariant audit (audit.sh)

`scripts/qa/audit.sh [--big] [--clean]` runs a tiered matrix of CLI commands and
auto-checks invariants (geometry, pyramid, codec, subsampling tag-vs-bytes,
metadata sanity + cross-command/cross-container consistency) plus OpenSlide/
Bio-Formats openability. Outputs `report.md` under `$OUT` (default
`/Volumes/Ext/tmp/wsitools-audit`). Outputs are deleted per-case to bound disk.
Triage `report.md` by hand — it catalogues, it does not file issues.
```

```bash
git add scripts/qa/audit.sh scripts/qa/MANUAL-TEST-PLAN.md
git commit -m "qa(audit): audit.sh entry point + docs; full harness wired"
```

---

### Task 11: Round-trip pixel-identity + associated-image edit cases

**Files:**
- Modify: `scripts/qa/audit_cases.py` (associated-edit cases)
- Modify: `scripts/qa/audit_run.py` (round-trip check)
- Test: `scripts/qa/tests/test_audit_cases.py` (append)

Context: closes the two remaining spec families — lossless A→B→A must be
pixel-identical, and the associated-image editors (`label|macro|thumbnail|
overview remove|replace`) must leave a reopenable slide with the edit applied.

- [ ] **Step 1: Add associated-edit cases (append inside `enumerate_cases`, before `return cases`)**

```python
    # --- Associated-image edits on the small SVS (label/macro/thumbnail/overview) ---
    if small:
        # A tiny PNG to use as replacement content.
        for typ in ("label", "macro", "thumbnail", "overview"):
            out = f"OUTDIR/assoc_rm_{typ}.svs"
            add(f"assoc-rm-{typ}", [typ, "remove", "-f", "-o", out, small],
                small, "svs", out, "associated-edit", expect_error=False)
```

- [ ] **Step 2: Add a test for the associated-edit cases (append to test_audit_cases.py)**

```python
def test_associated_edit_cases_present(tmp_path):
    (tmp_path / "svs").mkdir()
    (tmp_path / "svs" / "CMU-1-Small-Region.svs").write_bytes(b"x")
    cases = audit_cases.enumerate_cases(str(tmp_path), big=False)
    edits = [c for c in cases if c.transform_type == "associated-edit"]
    assert {c.cmd_argv[0] for c in edits} >= {"label", "macro", "thumbnail", "overview"}
```

Run: `cd scripts/qa && python3 -m pytest tests/test_audit_cases.py -q` → PASS (3 passed).
Note: the associated-edit output is checked by the *existing* orchestrator path
(openability + `check_metadata_sanity`); a `remove` that leaves the pyramid intact
and drops the associated type produces no finding, a corrupted one does.

- [ ] **Step 3: Add the round-trip pixel-identity check to `audit_run.py`**

Add this function and call it from `run()` after the main case loop (before the
cross-container block). It re-runs conversions itself, so it is independent of the
enumerated single-command cases:

```python
def _pixel_hash(binp: str, path: str) -> str | None:
    # Confirmed schema: {algorithm, mode, hex, path}; the digest is in "hex".
    j = _wsi_json(binp, ["hash", "--mode", "pixel", "--json", path])
    return j.get("hex") if j else None


def roundtrip_check(fixtures: str, cases_dir, binp: str, emit) -> None:
    """Lossless A→svs→A': pixel hash must be identical. Uses the small SVS."""
    src = os.path.join(fixtures, "svs", "CMU-1-Small-Region.svs")
    if not os.path.exists(src):
        return
    h0 = _pixel_hash(binp, src)
    mid = str(cases_dir / "rt_mid.tiff")
    back = str(cases_dir / "rt_back.svs")
    # Lossless round-trip: tile-copy to tiff, then tile-copy back to svs.
    subprocess.run([binp, "convert", "--to", "tiff", "-f", "-o", mid, src], capture_output=True)
    subprocess.run([binp, "convert", "--to", "svs", "-f", "-o", back, mid], capture_output=True)
    h1 = _pixel_hash(binp, back)
    if h0 and h1 and h0 != h1:
        emit(Finding("roundtrip-svs-tiff-svs", "roundtrip", "lossless-pixel-identity",
            "silent-wrong-output", f"pixel hash {h0}", h1,
            "wsitools convert --to tiff … && convert --to svs …"))
    _cleanup(mid)
    _cleanup(back)
```

In `run()`, before the `for src, per in swap_group.items():` loop, add:
`roundtrip_check(fixtures, cases_dir, binp, emit)`.

- [ ] **Step 4: Sanity-confirm the hash digest key**

Run: `bin/wsitools hash --mode pixel --json sample_files/svs/CMU-1-Small-Region.svs 2>/dev/null`
Expected: `{"algorithm":"sha256","mode":"pixel","hex":"…","path":"…"}` — the digest is in `hex` (confirmed schema, already used by `_pixel_hash`).

- [ ] **Step 5: Smoke-run + commit**

Run: `scripts/qa/audit.sh --clean` → completes; if the round-trip is genuinely
lossless the report has no `lossless-pixel-identity` finding.

```bash
git add scripts/qa/audit_cases.py scripts/qa/audit_run.py scripts/qa/tests/test_audit_cases.py
git commit -m "qa(audit): round-trip pixel-identity + associated-edit cases"
```

---

## Final step (human-in-the-loop, not a task)

Run `scripts/qa/audit.sh` (and `--big` for NDPI/IFE), then **triage `report.md`
together**: dedup against the known/fixed issues (#24 jpegxl-undecodable will
likely surface as an openability/round-trip finding; #25 DZI-cancel is out of
static scope), and open issues for the genuinely new discrepancies.
