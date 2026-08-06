# ADR-0157 — Nothing may write into a directory nginx includes

- **Status:** Accepted; shipped in v3.17.14
- **Date:** 2026-08-06
- **Follows** ADR-0155 (certificates without a restart) and ADR-0156 (the write
  connection). Both were real defects. Neither was this one.

## 0. Three wrong answers first

An operator reported, four times over two days, that adding a domain and
provisioning its certificate took the site to 502 for minutes, after which it
recovered on its own. Three fixes shipped against that report:

| Release | What was fixed | Was it the cause? |
| --- | --- | --- |
| v3.17.11 | three unnecessary nginx/app restarts removed | no |
| v3.17.12 | helpers opened the DB with the server's full init | no |
| v3.17.13 | traffic queued on the single write connection | no |

Every one of those was a genuine defect, found by reading code and proved by a
test. **Not one of them was diagnosed from the failure.** The pattern is the
finding: a mechanism that plausibly produces the reported symptom is not
evidence that it produced *this* symptom, and shipping against a plausible
mechanism three times in a row is how two days go by with the fault untouched.

What ended it was one probe against the running install during an outage.

## 1. The evidence, in the order it landed

**The app was never the problem.** During a 502, on the box:

```
$ curl -v -m 8 http://127.0.0.1:8080/health
{"status":"alive","version":"3.17.13","uptime_seconds":884.05}
```

That endpoint touches no database. It answered in **1 ms**. The journal showed
continuous service throughout — 200s, 304s and 404s at 1–35 ms. Nothing was
blocked, stalled or queued. Every theory in the table above was excluded by a
single request.

**nginx never generated a 502 either.** `grep ' 502 ' access.log` returned
nothing at all. Whatever the browser was rendering, nginx did not produce it.

**What the error log did show**, clustered at the exact minute the provisioning
run wrote its vhosts:

```
09:11:13 [alert] *9550 open socket #17 left in connection 10
09:11:13 [alert] aborting
09:11:17 [alert] aborting
09:11:20 [alert] aborting
09:11:31 [alert] aborting
```

**And, repeated on every reload for three days:**

```
[warn] conflicting server name "mcp.johal.in" on 0.0.0.0:443, ignored
```

**Then the directory listing that explained it:**

```
lrwxrwxrwx  vayupress-mcp -> /etc/nginx/sites-available/vayupress-mcp
-rw-r--r--  vayupress-mcp.vayushield.bak      <-- a REGULAR FILE, dated 3 Aug
```

Every other entry was a symlink.

## 2. Defect A — a backup was live configuration

`deploy/vayushield-agent.sh` globs `/etc/nginx/sites-enabled/*` to find the MCP
vhost, then saved its pre-change copy beside it:

```bash
found=…                                   # from a glob over sites-enabled
local bak="${found}.vayushield.bak"
cp -f "$found" "$bak"
```

nginx includes that directory with `include /etc/nginx/sites-enabled/*;` — a
bare glob, no extension filter, no exclusions. **The backup was not a backup.**
It was a second live server block from the moment it was written, and it had
been parsed on every reload for three days.

nginx resolves two blocks claiming one name by keeping whichever the glob
reached first and discarding the other, and says so once, at warn level. Which
one loses depends on filename ordering — which is to say, on nothing an operator
would ever think to check.

The other two backup sites in that script write into `conf.d/`, which is included
as `*.conf`; a `.conf.vayushield.bak` never matched, so those were inert. **Only
the `sites-enabled` one was live** — and it is the only one whose path came from
a glob over an include directory, which is exactly the coupling that made it
dangerous.

## 3. Defect B — the reload storm

`scripts/setup-vayudomain.sh` reloads nginx **twice per host**: once for the
HTTP-only vhost that lets certbot validate, once for the real vhost after the
certificate exists. Six domains is twelve reloads, and the mcp/api/talk/
openpgpkey helpers add their own — sixteen in about ninety seconds on the
install that reported this.

Each reload starts a worker generation and asks the previous one to drain. Issue
the next before that finishes and generations pile up, until nginx stops waiting:
`open socket … left in connection` then `aborting` is a worker being retired
while it still holds live connections. **Every request in flight on it dies
mid-response.** The client sees a dropped connection, and anything in front of
nginx renders that as 502 — which is why nginx's own access log recorded none.

Nothing here is nginx misbehaving. It is being asked to reload faster than it can
retire a generation, which is a thing only the caller can fix.

## 4. What was built

**A backup never lands where configuration is read from.** `shield_backup`
writes to a dedicated directory outside every include path. Existing installs are
repaired rather than only new ones: `sweep_stray_nginx_backups` runs on every
reconcile and moves `.bak`, `.save`, `.orig`, `.dpkg-old`, `.dpkg-dist` and `~`
files out of `sites-enabled`. Only regular files, and only those suffixes — a
symlink is how a vhost is *enabled*, and a plainly-named regular file may be an
operator's hand-written vhost. Sweeping either would take a site down to fix a
tidiness problem.

**Reloads no longer overlap.** `await_nginx_drain` waits for the previous
generation before another reload is issued. The wait is observed rather than
guessed — nginx renames a draining worker's process title to
`worker process is shutting down` — so on the common case of a single-domain
install with nothing draining it costs nothing at all. A genuinely long-lived
connection is capped, and proceeding past the cap says so out loud, because that
is the one remaining path that can still drop a request.

**The panel names the collision.** The Sites page reports any hostname declared
by more than one file, naming both files, and any backup sitting in the include
path. It renders *nothing* on a healthy install — this exists to surface a fault
that lived in a warn-level log line, and a permanent green panel for a condition
almost nobody has is the same noise that made the original warning invisible.

Two things the checker deliberately does not do: it does not parse nginx's
grammar or resolve includes, and it does not predict which block wins. That
depends on glob order and listen addresses, and a panel that guessed would be
worse than one that stays quiet.

## 5. What the audit found — in this change's own code

**A local privilege-escalation primitive, introduced by the fix.** The first
version put the backup directory at `/var/lib/vayupress/nginx-backups`. That tree
is owned by the **unprivileged service user**; the agent doing the moving runs as
**root**. So the service user could pre-create `nginx-backups` as a symlink and
root's own `mv` would follow it, depositing files into any directory they chose.
The fix for an outage would have shipped an escalation alongside it.

It moved to `/var/backups`, which is root-owned on every Debian-family install —
removing the primitive rather than guarding it — and a symlinked destination is
refused outright as defence in depth.

The first mutation written for that guard **survived**, and the mutation was
wrong rather than the test: it removed one of two redundant symlink checks and
the second still caught the plant. Re-run with both removed, the test failed
exactly as it should, naming the file root had been made to write.

**An unbounded read on a page view.** The checker reads every file in the
directory — including through symlinks, as nginx does — on every Sites page load.
Capped at 512 KB per file. A generated vhost here is under 4 KB.

Attacked and found sound: the sweep's suffix list against a hand-written vhost
and against a symlink (both correctly left alone); the duplicate detection
against a normal vhost, which declares its hostname twice, once per listen block,
and must not be flagged; the catch-all `server_name _`, which every install
shares by design; and the card's escaping, against a hostile filename and a
hostile `server_name`.

## 6. How it was proven

Nine mutations across the shell and Go changes, all killed. The shell functions
are **executed** against fixture directories rather than read for strings — a
source-level check would pass on a script that contains the right words and does
the wrong thing, which is how a backup came to live in an include path in the
first place. `await_nginx_drain` is exercised against a stubbed `ps` that reports
a draining worker three times and then a clean table.

One test-quality note, because it repeats a lesson from ADR-0156: the duplicate
check initially anchored `server_name` to the start of a line. That silently
missed `server { listen 80; server_name x; }` written on one line — valid nginx,
and the format of this product's own generated vhosts. A check that only saw
tidily-formatted configuration would have reported the incident that prompted it
as clean.

## 7. The rule this ADR is named for

**Nothing may write a file into a directory another program reads with a glob.**
Not a backup, not a temp file, not a `.orig`. The reader's include pattern is not
part of the writer's interface and may change without notice; a file's safety
must never depend on the exact shape of somebody else's glob.

And the process lesson, which cost more than the bug: **a plausible mechanism is
not a diagnosis.** Three correct fixes shipped against a fault none of them
touched. The probe that settled it — one request to an endpoint that needs no
database, taken while the site was actually failing — was available on the first
report and should have been the first thing asked for.
