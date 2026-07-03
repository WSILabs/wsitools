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
    assert md.index("silent-wrong-output") < md.index("metadata-sanity")
    assert "output-codec-honored" in md and "2 case" in md
    assert "r1" in md and "r3" in md
