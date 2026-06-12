# ADR-0040: Config Versioning + Compatibility Contracts

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

VayuPress configuration is loaded from environment variables at startup. As the project evolves, new env vars are added and old ones are deprecated. Without a versioning scheme, operators upgrading from an old version may not know which env vars are new, deprecated, or changed in meaning.

## Decision

1. The config struct includes a `ConfigVersion` field, populated from the `VAYU_CONFIG_VERSION` env var (default: `"1"`).
2. At startup, `validateConfigVersion()` checks the loaded version against the compiled-in `currentConfigVersion` constant.
3. If the loaded version is older: log a `CONFIG_UPGRADE_NEEDED` warning listing the new required env vars introduced since that version.
4. If the loaded version is newer: log a `CONFIG_VERSION_MISMATCH` error and halt (binary is too old for the config).
5. The `docs/INSTALLATION.md` config table is the canonical reference for all env vars, including version-introduced annotations.
6. Deprecated env vars are supported for 2 major versions, then removed. Deprecation is logged at startup with `DEPRECATED_ENV_VAR` and the replacement.

## Rationale

Operators who upgrade the binary without reading the changelog need a safety net. The config version check is that net — it tells them exactly what changed rather than failing silently with a zero-value for a new required field.

## Consequences

- Positive: Upgrade friction is minimized; operators get actionable warnings.
- Positive: Compatibility contracts are enforceable in CI (check that version was bumped when new required vars are added).
- Negative: Requires discipline to bump `currentConfigVersion` when adding required env vars. A linter check in CI enforces this.
