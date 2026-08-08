#!/usr/bin/env python3
"""Fail the build before docs/site/.well-known/security.txt goes stale.

RFC 9116 requires an `Expires` field, and a security.txt past its expiry is
reported as stale by the scanners that read it — so an expired file is worse
than none: it becomes the finding it was meant to prevent.

The date used to be stamped at publish time by `.github/workflows/deploy-site.yml`,
which ran daily, so the committed value never mattered. vayupress.com is now
served from a VayuPress install rather than GitHub Pages, that workflow is gone,
and the file ships verbatim inside the self-hosted bundle
(`scripts/build-selfhosted-site.sh` copies the whole `docs/site/` tree). The date
in the file is now the date that gets served.

Nothing refreshes it any more, so something has to notice. This runs in CI and
fails while there is still time to act, rather than after the file has expired.

    python3 scripts/check-security-txt.py [--days N]
"""
import argparse
import datetime as dt
import pathlib
import re
import sys

FILE = pathlib.Path("docs/site/.well-known/security.txt")
# Long enough that a fix is never urgent, short enough that the warning is not
# so early it gets ignored.
DEFAULT_LEAD_DAYS = 45

# RFC 9116 §2.5.5: Expires is an ISO 8601 / RFC 3339 timestamp.
EXPIRES = re.compile(r"^Expires:\s*(\S+)\s*$", re.M)
CONTACT = re.compile(r"^Contact:\s*\S+\s*$", re.M)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--days", type=int, default=DEFAULT_LEAD_DAYS,
                    help="fail when the file expires within this many days")
    # Injectable so the gate's own failure can be tested without waiting for
    # real time to pass. A gate nobody has watched fail has proved nothing.
    ap.add_argument("--now", help="ISO timestamp to treat as now (testing)")
    args = ap.parse_args()

    if not FILE.exists():
        sys.exit("%s is missing — the RFC 9116 contact would 404" % FILE)

    body = FILE.read_text(encoding="utf-8")

    if not CONTACT.search(body):
        sys.exit("%s has no Contact: field; RFC 9116 requires at least one" % FILE)

    found = EXPIRES.findall(body)
    if len(found) != 1:
        sys.exit("%s has %d Expires: fields; RFC 9116 requires exactly one"
                 % (FILE, len(found)))

    raw = found[0]
    try:
        expires = dt.datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        sys.exit("%s: Expires is not a valid ISO 8601 timestamp: %r" % (FILE, raw))
    if expires.tzinfo is None:
        sys.exit("%s: Expires must carry a timezone offset: %r" % (FILE, raw))

    now = (dt.datetime.fromisoformat(args.now.replace("Z", "+00:00"))
           if args.now else dt.datetime.now(dt.timezone.utc))
    left = (expires - now).days

    if left < 0:
        sys.exit("%s EXPIRED %d day(s) ago (%s). Scanners report an expired "
                 "security.txt as a finding. Push the date out and re-release."
                 % (FILE, -left, raw))
    if left < args.days:
        sys.exit("%s expires in %d day(s) (%s), inside the %d-day lead time. "
                 "Push the date out now — nothing refreshes it automatically "
                 "since the site moved off GitHub Pages."
                 % (FILE, left, raw, args.days))

    print("security.txt valid for %d more day(s) (Expires: %s)" % (left, raw))


if __name__ == "__main__":
    main()
