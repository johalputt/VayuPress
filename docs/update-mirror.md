# The release mirror

Some hosting providers blackhole traffic to GitHub. A VayuPress install on such a
host saw every "Check for updates" end in `dial tcp 140.82.121.5:443: i/o timeout`
— DNS answered, the local firewall allowed outbound, and the route to GitHub was
simply dead. VayuPress's rule is that an operator without a shell is never stuck,
so the console works around it in three layers:

1. **The resilient transport** (`internal/safefetch`): every update check dials
   *all* of the host's resolved addresses for GitHub (IPv6 first, RFC 8305
   order) and, if every one fails, re-resolves through DNS-over-HTTPS.
2. **The endpoint chain** (`internal/update/endpoints.go`): GitHub → the
   official mirror → a metadata-only CDN listing. The panel shows a small
   "via mirror / via CDN" note so the operator can see which path answered.
3. **The mirror** (this document): <https://updates.vayupress.com>, a VayuPress
   install serving the releases it has downloaded and verified.

## What the mirror is

Any VayuPress install can serve the mirror from one of its hosted domains:
**VayuOS → the domain's page → Site administration → Release mirror**. The
official one runs on `updates.vayupress.com`, which is the address every install
from 3.17.66 onwards falls back to (`update.OfficialMirror`).

Switched on, the install:

- **syncs every hour** (`releaseMirrorInterval`, `cmd/vayupress/release_mirror.go`)
  with a conditional request, so an hour with no new release costs GitHub one
  `304`. Switching the mirror on, or pressing **Sync now**, starts a sync at
  once;
- **holds** the newest stable release, the stable release before it (so an
  install that must roll back still finds files), and the newest pre-release
  when it is ahead of both. Older releases are removed from disk;
- **verifies before it holds** (`verifyMirroredRelease`, `internal/update/mirror.go`).
  The binary must pass exactly what an install demands before it updates —
  checksum, Sigstore signature pinned to the release workflow on `main`, the
  Ed25519 release-key signature, and a Linux executable image — through the same
  functions `ApplyVerified` calls. Every other file must match the SHA-256
  GitHub recorded for it, plus its own `.sha256` and signature where the release
  ships them. A release that fails any check is not held and not advertised; the
  reason is on the domain's page and in `get_site`;
- **answers three paths** on that domain, from what it holds:
  `/api/github/repos/<owner>/<repo>/releases[/latest]` (GitHub's own JSON for the
  held releases), `/download/github/<owner>/<repo>/releases/download/<tag>/<file>`
  (the file), and `/mirror/status.json` (a public summary with no error text).
  Nothing is ever relayed to GitHub: an unknown repository, tag or file is a 404.

Everything else on the domain is served as before, so the domain's uploaded site
stays the page people see. The official page's source is in
[`deploy/updates-site/`](../deploy/updates-site/) and is published with the
`build_site` connector tool.

## Guarantees, and what enforces each

| Guarantee | Mechanism |
|---|---|
| A tampering mirror cannot change what installs | `ApplyVerified` checks the Sigstore signature, release key and checksum on every download, wherever it came from |
| The mirror never advertises a release installs would refuse | `verifyMirroredRelease` runs the installer's own checks before a release is held |
| The mirror is not an open GitHub relay | `Mirror.ServeHTTP` answers only from `state.json`; no request reaches an outbound call |
| The updater is never shown a browser challenge | `shieldBypass` exempts the mirror paths on a mirror domain; the operator's country and address refusals still apply first |
| One client cannot take the uplink | per-client download budget (20 files an hour, IPv6 grouped by /64) and 8 concurrent transfers |
| A slow host can finish a 56 MB download | the mirror lifts the 30-second write deadline to 30 minutes for file transfers |
| A Tor Space leaks nothing | the mirror neither syncs nor answers in onion mode |

## Pointing an install elsewhere

`VAYU_UPDATE_MIRROR` (environment) overrides the official mirror URL — for
example a mirror you serve from one of your own domains. Set it to `off` to
disable the mirror layer entirely (GitHub only).

## Installs on 3.17.64 and 3.17.65

Those two releases fell back to `https://updates.johal.in`, a hostname that never
resolved. An install on either version that cannot reach GitHub gets no mirror;
it updates normally once it reaches GitHub once, and from 3.17.66 onwards it
falls back here.
