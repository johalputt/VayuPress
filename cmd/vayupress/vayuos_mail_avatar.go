package main

// vayuos_mail_avatar.go — per-mailbox profile pictures (≤500 KB), stored as
// bytes in the account row and served from a same-origin route. Upload/remove
// are administrator-only (part of account management); the image is validated by
// magic bytes (not the client-supplied Content-Type) and size-capped.

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// maxMailAvatarBytes caps a stored profile picture at 500 KB (matches the mail
// package's maxAvatarBytes; kept here because that constant is unexported).
const maxMailAvatarBytes = 500 * 1024

// detectAvatarType identifies a PNG/JPEG/GIF/WebP image from its magic bytes and
// returns the canonical MIME. The client-supplied Content-Type is never trusted.
func detectAvatarType(b []byte) (string, bool) {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	case len(b) >= 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "image/gif", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	default:
		return "", false
	}
}

// avatarAccountEmail validates that the request targets a real mail account and
// returns its canonical email, or "" (having written the error) when not.
func (a *App) avatarAccountEmail(w http.ResponseWriter, r *http.Request, raw string) string {
	email := strings.ToLower(sanitizeMailUser(strings.TrimSpace(raw)))
	if email == "" || !strings.Contains(email, "@") {
		writeAPIError(w, r, http.StatusBadRequest, "bad-email", "invalid mailbox address", "")
		return ""
	}
	return email
}

// handleVayuOSAvatarUpload stores a mailbox's profile picture and returns the
// refreshed accounts list. Admin-only, CSRF-protected, multipart.
func (a *App) handleVayuOSAvatarUpload(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMailAvatarBytes+64*1024)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "too-large", "The image exceeds the 500 KB limit.", "")
		return
	}
	email := a.avatarAccountEmail(w, r, r.FormValue("email"))
	if email == "" {
		return
	}
	file, _, err := r.FormFile("avatar")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "no-file", "No image was provided.", "")
		return
	}
	defer file.Close()
	blob, _ := io.ReadAll(io.LimitReader(file, maxMailAvatarBytes+1))
	if len(blob) == 0 {
		writeAPIError(w, r, http.StatusBadRequest, "empty", "The image is empty.", "")
		return
	}
	if len(blob) > maxMailAvatarBytes {
		writeAPIError(w, r, http.StatusBadRequest, "too-large", "The image exceeds the 500 KB limit.", "")
		return
	}
	mime, ok := detectAvatarType(blob)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "bad-type", "Use a PNG, JPEG, GIF or WebP image.", "")
		return
	}
	if err := a.vayuMail.Accounts().SetAvatar(r.Context(), email, blob, mime); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", err.Error(), "")
		return
	}
	a.invalidateAvatarCache() // show the new picture across the mailbox at once
	writeOSHTML(w, a.vayuAccountsList(r.Context()))
}

// handleVayuOSAvatarRemove clears a mailbox's profile picture (back to initials)
// and returns the refreshed accounts list. Admin-only, CSRF-protected.
func (a *App) handleVayuOSAvatarRemove(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	email := a.avatarAccountEmail(w, r, r.FormValue("email"))
	if email == "" {
		return
	}
	if err := a.vayuMail.Accounts().ClearAvatar(r.Context(), email); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "clear-failed", err.Error(), "")
		return
	}
	a.invalidateAvatarCache() // drop the removed picture across the mailbox at once
	writeOSHTML(w, a.vayuAccountsList(r.Context()))
}

// handleFederatedAvatar serves a mailbox's profile picture over the public
// Libravatar/Gravatar-compatible endpoint GET /avatar/<hash>, where <hash> is the
// md5 or sha256 of the lowercased address. This is how avatar-federation-aware
// mail clients and services fetch a sender's picture, so a recipient can see it.
// Public by design (an avatar is meant to be seen); it exposes ONLY the image for
// an address that has opted in by uploading one, and 404s / honours the ?d=
// default otherwise.
func (a *App) handleFederatedAvatar(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if i := strings.IndexByte(hash, '.'); i >= 0 { // tolerate /avatar/<hash>.png
		hash = hash[:i]
	}
	serveDefault := func() {
		// Libravatar/Gravatar ?d= fallback: a URL redirects there, "404" 404s.
		d := strings.TrimSpace(r.URL.Query().Get("d"))
		if d == "" {
			d = strings.TrimSpace(r.URL.Query().Get("default"))
		}
		if strings.HasPrefix(d, "https://") || strings.HasPrefix(d, "http://") {
			http.Redirect(w, r, d, http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		serveDefault()
		return
	}
	blob, mime, err := a.vayuMail.Accounts().AvatarByHash(r.Context(), hash)
	if err != nil || len(blob) == 0 || mime == "" {
		serveDefault()
		return
	}
	w.Header().Set("Content-Type", mime)
	// Public and cacheable for an hour: federation fetches happen per recipient
	// view, so a short shared-cache lifetime keeps load down without pinning a
	// stale picture for long after a change.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(blob)
}

// handleVayuOSAvatarServe serves a mailbox's stored profile picture. Session-
// guarded (mounted on the OS group); no-cache so a freshly uploaded picture is
// shown immediately.
func (a *App) handleVayuOSAvatarServe(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		http.NotFound(w, r)
		return
	}
	email := strings.ToLower(sanitizeMailUser(strings.TrimSpace(r.URL.Query().Get("email"))))
	if email == "" {
		http.NotFound(w, r)
		return
	}
	blob, mime, err := a.vayuMail.Accounts().Avatar(r.Context(), email)
	if err != nil || len(blob) == 0 || mime == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(blob)
}
