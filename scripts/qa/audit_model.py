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
