package main

// handlers_member_avatar.go — reader (member) profile pictures. A signed-in
// member (free or paid) can upload a real photo (≤100 KB), pick a prebuilt
// cartoon, or fall back to a deterministic, gender-aware auto avatar. The avatar
// is served publicly by member id so it can appear on comments; the id is an
// opaque random token and the endpoint returns only the chosen picture (never the
// email, name, or any other field), so nothing private is exposed.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/members"
)

// handleMemberAvatarServe serves a member's avatar by id: their uploaded photo,
// their chosen cartoon, or the auto avatar — whichever is selected. Public and
// short-cacheable; the URL carries a ?v= token so a change busts caches at once.
func (a *App) handleMemberAvatarServe(w http.ResponseWriter, r *http.Request) {
	if a.members == nil {
		http.NotFound(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if i := strings.IndexByte(id, '.'); i >= 0 { // tolerate /avatar/<id>.svg
		id = id[:i]
	}
	info, err := a.members.AvatarByID(r.Context(), id)
	if err != nil || info == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=120")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// A real uploaded photo.
	if info.Choice == members.AvatarPhoto && len(info.Blob) > 0 && info.Mime != "" {
		w.Header().Set("Content-Type", info.Mime)
		_, _ = w.Write(info.Blob)
		return
	}
	// A prebuilt cartoon, or the deterministic auto avatar.
	var svg string
	if n, ok := cartoonIndex(info.Choice); ok {
		svg = avatar.Cartoon(n, info.Email)
	} else {
		svg = avatar.Auto(info.Email, info.Gender)
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = io.WriteString(w, svg)
}

// cartoonIndex parses a "cartoon:N" choice into a valid index.
func cartoonIndex(choice string) (int, bool) {
	rest, ok := strings.CutPrefix(choice, "cartoon:")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 0 || n >= avatar.CartoonCount {
		return 0, false
	}
	return n, true
}

// handleMemberAvatarUpload stores a member's uploaded photo (≤100 KB, common
// image types only). Authenticated by the member session; the member cookie is
// SameSite=Lax, so a cross-site POST never carries it (CSRF-safe), matching the
// other member-account endpoints.
func (a *App) handleMemberAvatarUpload(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "not-signed-in", "Sign in to set an avatar", "")
		return
	}
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	const cap = members.MaxAvatarBytes
	r.Body = http.MaxBytesReader(w, r.Body, cap+4096)
	if err := r.ParseMultipartForm(cap + 4096); err != nil {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "too-large", "Image must be 100 KB or smaller", "")
		return
	}
	f, _, err := r.FormFile("avatar")
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "no-file", "No image was uploaded", "")
		return
	}
	defer f.Close()
	blob, err := io.ReadAll(io.LimitReader(f, cap+1))
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "read-failed", "Could not read the image", "")
		return
	}
	if len(blob) == 0 {
		writeAPIError(w, r, http.StatusBadRequest, "empty", "The image was empty", "")
		return
	}
	if len(blob) > cap {
		writeAPIError(w, r, http.StatusRequestEntityTooLarge, "too-large", "Image must be 100 KB or smaller", "")
		return
	}
	mime := http.DetectContentType(blob)
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		writeAPIError(w, r, http.StatusUnsupportedMediaType, "bad-type", "Please upload a PNG, JPEG, WebP or GIF image", "")
		return
	}
	if err := a.members.SetAvatarPhoto(r.Context(), m.Email, blob, mime); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", "Could not save your avatar", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "choice": members.AvatarPhoto})
}

// handleMemberAvatarChoose selects which avatar the member shows (auto / photo /
// a prebuilt cartoon) and records their optional gender (steers the auto avatar).
func (a *App) handleMemberAvatarChoose(w http.ResponseWriter, r *http.Request) {
	m := a.resolveMember(r)
	if m == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "not-signed-in", "Sign in to set an avatar", "")
		return
	}
	if a.members == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "members-disabled", "Memberships not initialised", "")
		return
	}
	var body struct {
		Choice string `json:"choice"`
		Gender string `json:"gender"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request", "")
		return
	}
	choice := strings.TrimSpace(body.Choice)
	if _, ok := cartoonIndex(choice); !ok && choice != "" && choice != members.AvatarPhoto {
		writeAPIError(w, r, http.StatusBadRequest, "bad-choice", "Unknown avatar choice", "")
		return
	}
	gender := ""
	switch strings.ToLower(strings.TrimSpace(body.Gender)) {
	case "male", "female", "neutral":
		gender = strings.ToLower(strings.TrimSpace(body.Gender))
	}
	if err := a.members.SetAvatarChoice(r.Context(), m.Email, choice); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", "Could not save your avatar", "")
		return
	}
	_ = a.members.SetGender(r.Context(), m.Email, gender)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "choice": choice, "gender": gender})
}
