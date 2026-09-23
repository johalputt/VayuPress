#!/usr/bin/env python3
"""Refuse tracked text files that carry a UTF-8 BOM or double-encoded UTF-8.

Why this exists
---------------
An editor that reads UTF-8 as Windows-1252 and saves it back turns every
non-ASCII character into two or three Latin-1 ones: U+203A "›" becomes the
three characters U+00E2 U+20AC U+00BA. Everything still compiles, every test
still passes, and the console then shows those three beside every tool on
every per-site page. It shipped
that way in 3.17.64, and the same damage had already sat in comments since
before 3.17.61, because nothing looked.

Detection is exact rather than heuristic: a run of two or more characters from
the Windows-1252 upper half is flagged only when re-encoding it to 1252 bytes
yields valid UTF-8. Real text in those ranges (a lone "—", "%PDF-1.7 âãÏÓ")
does not decode that way, so it is left alone.

Usage
-----
    python3 scripts/check-encoding.py            # repair files in place
    python3 scripts/check-encoding.py --check    # CI: list offenders, exit 1
"""

import re
import subprocess
import sys

# Windows-1252 leaves five bytes undefined; editors pass them through as the
# matching C1 control, so map those too or "â€" + U+009D would never reverse.
TO_1252 = {}
for b in range(256):
    try:
        TO_1252[bytes([b]).decode("cp1252")] = b
    except UnicodeDecodeError:
        TO_1252[chr(b)] = b

UPPER = "".join(c for c in TO_1252 if ord(c) >= 0x80)
RUN = re.compile("[%s]{2,}" % re.escape(UPPER))


def reverse(m):
    s = m.group(0)
    try:
        return bytes(TO_1252[c] for c in s).decode("utf-8")
    except UnicodeDecodeError:
        return s


def main():
    check = "--check" in sys.argv[1:]
    names = subprocess.run(
        ["git", "ls-files", "-z"], capture_output=True, check=True
    ).stdout.decode().split("\0")
    bad = []
    for name in filter(None, names):
        try:
            with open(name, "rb") as f:
                text = f.read().decode("utf-8")
        except (UnicodeDecodeError, OSError):
            continue  # binary, or deleted in the working tree
        fixed = RUN.sub(reverse, text.lstrip("﻿"))
        if fixed == text:
            continue
        bad.append(name)
        if not check:
            with open(name, "wb") as f:
                f.write(fixed.encode("utf-8"))
    for name in bad:
        print(("mis-encoded: " if check else "repaired: ") + name)
    if check and bad:
        print("run: python3 scripts/check-encoding.py", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
