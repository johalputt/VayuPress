# AGENTS.md — instructions for AI agents working in this repo

## Releases: patch/micro only

**Only ever cut micro (patch) releases.** Bump the THIRD version segment and
never the minor or major segment — no matter how large the change feels.

- `v3.13.0` → `v3.13.1` ✅
- `v3.13.0` → `v3.14.0` ❌ (minor — do not do this)
- `v3.13.0` → `v4.0.0` ❌ (major — do not do this)

When releasing, update **all three** in the same commit so they match, then push:

- `.release-version` (e.g. `v3.13.1`) — changing this file triggers the
  tag-release workflow.
- `cmd/vayupress/main.go` → `var Version = "3.13.1"` (no `v` prefix).
- a matching `## [3.13.1]` section in `CHANGELOG.md`.
