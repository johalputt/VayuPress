# AGENTS.md — instructions for AI agents working in this repo

## Releases: micro by default, roll over at 99

**Default: bump the micro (THIRD) segment** — `v3.13.96 → v3.13.97`. Do not jump
minor or major just because a change feels large; size never drives the bump,
only the rollover rule below does.

**Roll over at 99** (each segment counts 0–99 and never exceeds 99):

- Micro is at **99** → the next release bumps the **minor** (second) segment and
  resets micro to 0: `v3.13.99 → v3.14.0`.
- Minor is at **99** and micro rolls over → the next release bumps the **major**
  (first) segment and resets minor and micro to 0: `v3.99.99 → v4.0.0`.

So the sequence runs: `…97 → 98 → 99 → v3.14.0 → v3.14.1 → … → v3.14.99 →
v3.15.0 → … → v3.99.99 → v4.0.0`.

- `v3.13.96` → `v3.13.97` ✅ (normal micro bump)
- `v3.13.99` → `v3.14.0` ✅ (micro hit 99 → roll to next minor)
- `v3.99.99` → `v4.0.0` ✅ (minor hit 99 and micro rolled → roll to next major)
- `v3.13.40` → `v3.14.0` ❌ (do NOT bump minor early — only at micro 99)

When releasing, update **all three** in the same commit so they match, then push:

- `.release-version` (e.g. `v3.13.1`) — changing this file triggers the
  tag-release workflow.
- `cmd/vayupress/main.go` → `var Version = "3.13.1"` (no `v` prefix).
- a matching `## [3.13.1]` section in `CHANGELOG.md`.
