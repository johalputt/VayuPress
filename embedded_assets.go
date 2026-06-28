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
