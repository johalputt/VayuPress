# ADR-0039: Deploy Sourced Component Architecture

**Status**: Accepted  
**Date**: 2026-06-12  
**Author**: @johalputt

## Context

The VayuPress deploy script embeds the full Go `main.go` source as a heredoc. This creates a single-file deploy artifact: download the script, run it, get a working server. However, as the deploy script grows (now ~4,000 lines), it becomes difficult to read and maintain if all components are interleaved.

## Decision

1. The deploy script is organized into **sourced component sections** with clear delimiters: `## === COMPONENT: <name> ===`.
2. Components are: `go-source`, `nginx-config`, `systemd-units`, `cron-jobs`, `smoke-tests`.
3. Each component section is self-contained: it defines its own variables, creates its own files, and handles its own error conditions.
4. The main script body calls component functions in order, with `set -euo pipefail` active throughout.
5. The `--dry-run` flag causes each component to print what it would do without making filesystem changes.

## Rationale

Sourced components with clear section boundaries allow a maintainer to find and modify the nginx config without reading the Go source, or update systemd units without touching cron jobs. The `--dry-run` flag makes the script safe to audit on production servers.

## Consequences

- Positive: Each component is independently readable and testable.
- Positive: `--dry-run` enables safe pre-deployment validation.
- Positive: `shellcheck` can verify each section independently.
- Negative: The script remains a single file — very long. Alternative: split into multiple files. This was rejected because it complicates the single-download deploy UX.
