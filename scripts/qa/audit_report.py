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
            for it in items[:8]:
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
