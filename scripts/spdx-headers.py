#!/usr/bin/env python3
"""Add or verify the SPDX licence identifier at the top of every Go source file.

Why this exists
---------------
Apache-2.0 is declared once in ``LICENSE`` and once in ``NOTICE``. That is enough
for a human and not enough for a machine. Software-composition scanners, SBOM
generators and the vulnerability tooling that consumes them read per-file
identifiers, and a file with no identifier is reported as "unknown licence" --
which, to anyone evaluating whether they may depend on VayuPress, is
indistinguishable from "proprietary".

The header is the SPDX identifier alone, with no copyright line. Copyright in
this repository is held by each contributor over their own work (DCO, no
assignment -- see docs/LICENSING.md), so a blanket per-file copyright notice
naming one person would be inaccurate.

Usage
-----
    python3 scripts/spdx-headers.py            # add missing headers in place
    python3 scripts/spdx-headers.py --check    # exit 1 if any file lacks one

``--check`` is the CI gate. It is what stops a new file from being merged
without an identifier, which is the only way a repository this size stays
consistent.
"""

from __future__ import annotations

import argparse
import pathlib
import sys

SPDX_LINE = "// SPDX-License-Identifier: Apache-2.0"

# Directories that are not ours to annotate: vendored third-party source keeps
# whatever licence it arrived with, and Go's own convention is to leave
# testdata untouched because some of it is deliberately malformed.
SKIP_DIRS = {".git", "vendor", "node_modules", "testdata"}

# How far into a file to look before concluding the identifier is absent. A
# header buried below this is not serving its purpose anyway.
SCAN_LINES = 10


def go_files(root: pathlib.Path) -> list[pathlib.Path]:
    out = []
    for path in root.rglob("*.go"):
        if SKIP_DIRS.intersection(path.relative_to(root).parts):
            continue
        out.append(path)
    return sorted(out)


def has_identifier(text: str) -> bool:
    return any(
        "SPDX-License-Identifier:" in line
        for line in text.splitlines()[:SCAN_LINES]
    )


def add_identifier(text: str) -> str:
    """Insert the identifier at the very top, followed by a blank line.

    The blank line is not cosmetic. Without it the identifier would be absorbed
    into whatever comment block follows -- turning a package doc comment into
    prose that begins with a licence string, and sitting inside the comment
    group that carries a ``//go:build`` constraint. A blank line keeps the
    identifier its own comment group, which is correct in both cases.
    """
    return f"{SPDX_LINE}\n\n{text.lstrip(chr(10))}"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="report files missing the identifier and exit non-zero; change nothing",
    )
    parser.add_argument(
        "--root",
        default=".",
        help="repository root to scan (default: the current directory)",
    )
    args = parser.parse_args()

    root = pathlib.Path(args.root).resolve()
    missing: list[pathlib.Path] = []

    for path in go_files(root):
        text = path.read_text(encoding="utf-8")
        if has_identifier(text):
            continue
        missing.append(path)
        if not args.check:
            path.write_text(add_identifier(text), encoding="utf-8")

    rel = [str(p.relative_to(root)) for p in missing]

    if args.check:
        if missing:
            print(f"FAIL -- {len(missing)} Go file(s) have no SPDX identifier:")
            for name in rel[:40]:
                print(f"  {name}")
            if len(rel) > 40:
                print(f"  ... and {len(rel) - 40} more")
            print(f"\nRun: python3 scripts/spdx-headers.py")
            return 1
        print(f"OK -- every Go file carries {SPDX_LINE.split(': ')[1]}.")
        return 0

    if missing:
        print(f"Added the SPDX identifier to {len(missing)} file(s).")
    else:
        print("OK -- nothing to do; every Go file already carries an identifier.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
