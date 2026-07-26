// SPDX-License-Identifier: Apache-2.0

// Package vayupress is the module root. Its sole purpose is to compile the
// first-party static assets (the VayuOS admin CSS and JavaScript) into the
// binary.
//
// Why this exists (ADR-0099): the VayuOS one-click self-update swaps only the
// running executable. The admin panel's CSS/JS, however, were previously served
// straight from STATIC_DIR on disk and refreshed by a *separate* file-copy step
// in the deploy script. After a binary-only update the on-disk assets were
// therefore stale, so the new panel loaded old CSS/JS — exactly the kind of
// half-applied update we must never ship. Embedding the assets makes the binary
// the single source of truth: the new binary carries the new assets and writes
// them to STATIC_DIR on boot (see cmd/vayupress/static_sync.go), so "update the
// binary" updates everything, atomically, with no extra steps.
package vayupress

import "embed"

// StaticFS holds the contents of the repository static/ directory, embedded at
// build time. Members are rooted at "static/" (e.g. "static/js/admin-os.js").
//
//go:embed static
var StaticFS embed.FS

// DocsADRFS holds the repository docs/adr directory, embedded at build time so
// the VayuOS ADR Registry always lists every Architecture Decision Record
// shipped with the running binary. Before this was embedded, the registry read
// docs/adr straight from disk; a one-click binary self-update (ADR-0099) never
// refreshed those on-disk files, so a box provisioned at an older release kept
// showing a frozen, truncated ADR list. Embedding makes the binary the single
// source of truth (mirroring StaticFS). Members are rooted at "docs/adr/".
//
//go:embed docs/adr
var DocsADRFS embed.FS

// DocsFS holds the human-readable documentation tree (top-level guides plus the
// prose sub-folders), embedded at build time so the public /docs site — the
// operator- and auditor-facing home for VayuPress's docs — ships inside the one
// binary and updates atomically with a self-update, exactly like StaticFS and
// DocsADRFS. The Architecture Decision Records live in DocsADRFS already and are
// intentionally NOT re-embedded here; the docs handler unions the two. The
// image-heavy folders (docs/screenshots, docs/assets, docs/site) are excluded to
// keep the binary lean — /docs renders markdown, not the README screenshots.
// Members are rooted at "docs/" (e.g. "docs/OPERATIONS.md", "docs/security/trust-model.md").
//
//go:embed docs/*.md
//go:embed docs/architecture docs/compatibility docs/governance docs/operations docs/plugins docs/release docs/reliability docs/security
var DocsFS embed.FS
