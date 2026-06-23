#!/usr/bin/env python3
"""Validate IFE files against the official IrisDigitalPathology Iris-Codec
implementation (the IFE equivalent of dciodvfy). Exits non-zero if any file
fails validation. Usage: ife_validate.py FILE.iris [FILE.iris ...]"""
import sys
from Iris import Codec


def main(paths):
    rc = 0
    for p in paths:
        result = Codec.validate_slide_path(p)
        ok = result.success()
        msg = result.message() if callable(getattr(result, "message", None)) else result.message
        if ok:
            print(f"OK    {p}")
        else:
            print(f"FAIL  {p}: {msg}")
            rc = 1
    return rc


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
