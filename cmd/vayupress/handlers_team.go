package main

// handlers_team.go — staff team management, self-service author profiles, and
// the public author profile page.
//
// Roles: admin / editor / author. Admins manage the team (create accounts,
// change roles, delete) from the Members console; creating an account also
// auto-provisions a sovereign VayuMail mailbox + PGP keypair (publishUserCreated).
// Every staff member can edit their own public profile — display name, a short
// bio (<=250 chars), an avatar, and social links — which renders at
// /author/{id} for readers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"html"
	htmpl "html/template"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// socialPlatforms is the curated set of social links the profile editor offers.
// label -> human name. Values are stored/rendered as a label->URL map.
var socialPlatforms = []struct{ Key, Label, Placeholder string }{
	{"website", "Website", "https://example.com"},
	{"twitter", "X / Twitter", "https://x.com/you"},
	{"github", "GitHub", "https://github.com/you"},
	{"linkedin", "LinkedIn", "https://linkedin.com/in/you"},
	{"mastodon", "Mastodon", "https://mastodon.social/@you"},
	{"instagram", "Instagram", "https://instagram.com/you"},
	{"youtube", "YouTube", "https://youtube.com/@you"},
}

// authorSlug returns the public handle used in /author/<slug> for a user: the
// human-readable username when set, else the opaque id (back-compat).
func authorSlug(u *users.User) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return u.ID
}

func socialLabel(key string) string {
	for _, p := range socialPlatforms {
		if p.Key == key {
			return p.Label
		}
	}
	return titleFirst(key)
}

// titleFirst upper-cases the first rune of s, preserving the remainder.
func titleFirst(s string) string {
	if s == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[size:]
}

// =============================================================================
// Admin: role management
// =============================================================================

// roleBody is the JSON payload for a role change.
type roleBody struct {
	Role string `json:"role"`
}

// PUT /api/v1/admin/users/{email}/role  {role}
// PUT /os/api/users/{email}/role        {role}
func (a *App) handleUserSetRole(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	email := chi.URLParam(r, "email")
	var body roleBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Guard against an admin removing the last admin role and locking everyone
	// out of administration.
	if !users.ValidRole(body.Role) {
		writeAPIError(w, r, http.StatusBadRequest, "role-error", "invalid role (want admin, editor, or author)", "")
		return
	}
	if body.Role != users.RoleAdmin {
		if last, err := a.isLastAdmin(r, email); err == nil && last {
			writeAPIError(w, r, http.StatusBadRequest, "role-error", "cannot demote the last remaining admin", "")
			return
		}
	}
	if err := a.userStore.SetRole(r.Context(), email, body.Role); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "role-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"email": email, "role": body.Role})
}

// isLastAdmin reports whether email is the only admin account.
func (a *App) isLastAdmin(r *http.Request, email string) (bool, error) {
	list, err := a.userStore.List(r.Context())
	if err != nil {
		return false, err
	}
	admins, isTarget := 0, false
	for _, u := range list {
		if u.Role == users.RoleAdmin {
			admins++
			if strings.EqualFold(u.Email, email) {
				isTarget = true
			}
		}
	}
	return isTarget && admins <= 1, nil
}

// =============================================================================
// Admin: assign a VayuMail mailbox to a team member
// =============================================================================

// mapMailRole maps a CMS role to the corresponding VayuMail mailbox role.
func mapMailRole(cmsRole string) string {
	switch cmsRole {
	case users.RoleAdmin:
		return vmail.RoleAdministrator
	case users.RoleEditor:
		return vmail.RoleEditor
	default:
		return vmail.RoleAuthor
	}
}

// mailboxBody is the JSON payload for assigning a mailbox.
type mailboxBody struct {
	Local string `json:"local"`
	Pass  string `json:"pass"`
}

// POST /os/api/users/{email}/mailbox  {local, pass}
//
// Assigns (creates or re-passwords) a sovereign VayuMail mailbox
// "local@domain" to a team member and links it to their account so their mail
// panel scopes to it. Admin-only, CSRF-protected.
func (a *App) handleAssignMailbox(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	if a.userStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "users-disabled", "Accounts not initialised", "")
		return
	}
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active (set DOMAIN to enable mailboxes)", "")
		return
	}
	u, err := a.userStore.GetByEmail(r.Context(), chi.URLParam(r, "email"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No team member with that email", "")
		return
	}
	var body mailboxBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	local := strings.ToLower(strings.TrimSpace(body.Local))
	if local == "" || strings.ContainsAny(local, "@ \t") {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Enter a valid mailbox name (the part before @)", "")
		return
	}
	if len(body.Pass) < 8 {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "Password must be at least 8 characters", "")
		return
	}
	hash, err := auth.HashSecretArgon2id(body.Pass)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "hash-failed", "Could not hash password", "")
		return
	}
	email := local + "@" + a.vayuMail.Config().Domain
	role := mapMailRole(u.Role)
	accts := a.vayuMail.Accounts()

	// Create the account, or update the password + role if it already exists.
	if err := accts.Create(r.Context(), email, hash, u.Name, role); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			if perr := accts.SetPasswordHash(r.Context(), email, hash); perr != nil {
				writeAPIError(w, r, http.StatusBadRequest, "assign-failed", perr.Error(), "")
				return
			}
			_ = accts.SetRole(r.Context(), email, role)
			_ = accts.SetActive(r.Context(), email, true)
		} else {
			writeAPIError(w, r, http.StatusBadRequest, "assign-failed", err.Error(), "")
			return
		}
	}
	// Provision the Maildir folders and a PGP keypair (best-effort).
	_ = a.vayuMail.CreateMailbox("", local)
	if a.vayuPGP != nil {
		if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: u.Name, Email: email}); err != nil {
			logging.LogError("vayuos", "PGP keygen failed for assigned mailbox "+email, err.Error())
		}
	}
	// Link the mailbox to the CMS user so their panel scopes to it.
	if err := a.userStore.SetMailAddress(r.Context(), u.ID, email); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "link-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"email": email, "role": role})
}

// scopedMailUser resolves the mailbox identifier (local part) a request may
// operate on. Admins may target the requested mailbox; everyone else is locked
// to their own assigned mailbox (empty when none).
func (a *App) scopedMailUser(r *http.Request, requested string) string {
	if a.isAdminRequest(r) {
		return strings.TrimSpace(requested)
	}
	local, _ := a.ownMailbox(r)
	return local
}

// ownMailbox returns the signed-in user's assigned mailbox local part and full
// address, or ("","") when none is assigned (or the caller is an API-key
// session with no user).
func (a *App) ownMailbox(r *http.Request) (local, email string) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		return "", ""
	}
	if fresh, err := a.userStore.GetByID(r.Context(), u.ID); err == nil {
		u = fresh
	}
	email = strings.TrimSpace(u.MailAddress)
	if email == "" {
		return "", ""
	}
	local = email
	if i := strings.Index(local, "@"); i >= 0 {
		local = local[:i]
	}
	return local, email
}

// =============================================================================
// Self-service profile
// =============================================================================

// GET /os/profile — the signed-in staff member's profile editor.
func (a *App) handleOSProfile(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	u := currentUser(r)
	if u == nil {
		// API-key sessions have no associated user account to edit.
		body := `<div class="page-header"><h1>My profile</h1></div>
<div class="empty-state">Profile editing is available when you sign in with a user account.</div>`
		writeOSHTML(w, adminOSLayout(nonce, "My profile", "profile", cfg, htmpl.HTML(body)))
		return
	}
	// Reload to get the freshest profile fields.
	if fresh, err := a.userStore.GetByID(r.Context(), u.ID); err == nil {
		u = fresh
	}
	esc := html.EscapeString

	socialFields := ""
	for _, p := range socialPlatforms {
		val := ""
		if u.Socials != nil {
			val = u.Socials[p.Key]
		}
		socialFields += `<label class="field mt-3"><span class="field-label">` + esc(p.Label) + `</span>
      <input class="input" type="url" data-social="` + p.Key + `" value="` + esc(val) + `" placeholder="` + esc(p.Placeholder) + `"></label>`
	}

	avatarPreview := `<div class="pf-avatar-frame" data-avatar-frame>`
	if u.AvatarURL != "" {
		avatarPreview += `<img class="pf-avatar" data-avatar-preview src="` + esc(u.AvatarURL) + `" alt="Your avatar">`
	} else {
		avatarPreview += `<img class="pf-avatar" data-avatar-preview alt="" hidden>` +
			`<span class="pf-avatar-empty" data-avatar-empty>No photo</span>`
	}
	avatarPreview += `</div>`

	body := `<div class="page-header"><h1>My profile</h1>
<span class="muted text-sm">Your public author profile — shown at <a href="/author/` + esc(authorSlug(u)) + `">/author/` + esc(authorSlug(u)) + `</a></span></div>
<div class="card">
  <form data-profile-form>
    <div class="pf-head">` + avatarPreview + `<div>
      <div class="muted text-sm">Signed in as</div>
      <div class="row-title">` + esc(u.Email) + ` <span class="badge badge--ok">` + esc(roleDisplay(u.Role)) + `</span></div>
    </div></div>
    <label class="field mt-4"><span class="field-label">Display name</span>
      <input class="input" type="text" data-p-name value="` + esc(u.Name) + `" maxlength="250" required></label>
    <label class="field mt-3"><span class="field-label">About you <span class="muted">(max 250 characters)</span></span>
      <textarea class="input" rows="3" data-p-bio maxlength="250" placeholder="A short bio shown on your public profile.">` + esc(u.Bio) + `</textarea>
      <span class="field-hint" data-bio-count></span></label>
    <label class="field mt-3"><span class="field-label">Avatar image URL <span class="muted">(shown cropped to a circular thumbnail)</span></span>
      <input class="input" type="url" data-p-avatar value="` + esc(u.AvatarURL) + `" placeholder="https://…/photo.jpg"></label>
    <div class="card-subtitle mt-4">Social links</div>
    ` + socialFields + `
    <div class="vm-row mt-4">
      <button class="btn btn--primary" type="submit">Save profile</button>
      <a class="btn" href="/author/` + esc(authorSlug(u)) + `" target="_blank" rel="noopener">View public profile ↗</a>
      <span class="muted text-sm" data-p-status></span>
    </div>
  </form>
</div>
<script nonce="` + nonce + `" src="/os/static/js/admin-os-profile.js?v=` + assetVer("js/admin-os-profile.js") + `"></script>`
	writeOSHTML(w, adminOSLayout(nonce, "My profile", "profile", cfg, htmpl.HTML(body)))
}

// profileSaveBody is the JSON payload from the profile editor.
type profileSaveBody struct {
	Name    string            `json:"name"`
	Bio     string            `json:"bio"`
	Avatar  string            `json:"avatar_url"`
	Socials map[string]string `json:"socials"`
}

// POST /os/api/profile — save the signed-in member's profile.
func (a *App) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "a signed-in user account is required", "")
		return
	}
	var body profileSaveBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.userStore.UpdateProfile(r.Context(), u.ID, body.Name, body.Bio, body.Avatar, body.Socials); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "profile-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// =============================================================================
// Team card (rendered inside the Members console for admins)
// =============================================================================

// teamCardHTML renders the staff/team management card: the roster with inline
// role selectors and delete actions, plus a create form. Returns "" when the
// caller is not an admin so the card stays admin-only.
func (a *App) teamCardHTML(r *http.Request) string {
	if a.userStore == nil || !a.isAdminRequest(r) {
		return ""
	}
	esc := html.EscapeString
	list, _ := a.userStore.List(r.Context())
	mailEnabled := a.vayuMail != nil && a.vayuMail.Config().Enabled
	mailDomain := ""
	if mailEnabled {
		mailDomain = a.vayuMail.Config().Domain
	}

	roleOptions := func(current string) string {
		opts := ""
		for _, role := range []string{users.RoleAdmin, users.RoleEditor, users.RoleAuthor} {
			sel := ""
			if role == current {
				sel = " selected"
			}
			label := strings.ToUpper(role[:1]) + role[1:]
			opts += `<option value="` + role + `"` + sel + `>` + label + `</option>`
		}
		return opts
	}

	rows := ""
	for _, u := range list {
		name := esc(u.Name)
		if name == "" {
			name = `<span class="row-meta">—</span>`
		}
		mailbox := `<span class="row-meta">mail off</span>`
		if mailEnabled {
			if u.MailAddress != "" {
				mailbox = `<span class="badge badge--ok">` + esc(u.MailAddress) + `</span> ` +
					`<button class="btn btn--xs btn--ghost" type="button" data-assign-mailbox data-email="` + esc(u.Email) + `" data-domain="` + esc(mailDomain) + `" data-current="` + esc(u.MailAddress) + `">Change</button>`
			} else {
				mailbox = `<button class="btn btn--xs" type="button" data-assign-mailbox data-email="` + esc(u.Email) + `" data-domain="` + esc(mailDomain) + `">Assign email</button>`
			}
		}
		rows += `<tr>
  <td class="row-title"><a href="/author/` + esc(authorSlug(&u)) + `" target="_blank" rel="noopener">` + esc(u.Email) + `</a></td>
  <td>` + name + `</td>
  <td><select class="select input--sm" data-user-role data-email="` + esc(u.Email) + `">` + roleOptions(u.Role) + `</select></td>
  <td>` + mailbox + `</td>
  <td class="row-actions"><button class="btn btn--sm btn--danger" type="button" data-delete-user data-email="` + esc(u.Email) + `">Remove</button></td>
</tr>`
	}
	table := `<div class="empty-state">No team members yet.</div>`
	if rows != "" {
		table = `<div class="table-wrap"><table class="table">
  <thead><tr><th>Email</th><th>Name</th><th>Role</th><th>VayuMail</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody></table></div>`
	}

	mailNote := `Set <code>DOMAIN</code> to auto-provision a sovereign VayuMail mailbox for each new account.`
	if a.vayuMail != nil && a.vayuMail.Config().Enabled {
		mailNote = `New accounts are auto-provisioned a sovereign VayuMail mailbox (<code>name@` + esc(a.vayuMail.Config().Domain) + `</code>) and a PGP keypair. Manage mailboxes &amp; passwords under <a href="/os/vayuos/accounts">VayuMail → Mail accounts</a>.`
	}

	return `<div class="card mb-6">
  <div class="card-head">
    <h2 class="card-title">Team &amp; roles</h2>
    <span class="badge badge--muted">admin only</span>
  </div>
  <p class="field-hint">Roles: <strong>Admin</strong> manages the team &amp; settings · <strong>Editor</strong> writes &amp; manages all content · <strong>Author</strong> writes their own content. Each member edits their public profile under <a href="/os/profile">My profile</a>.</p>
  ` + table + `
  <div class="card-subtitle mt-4">Add a team member</div>
  <form data-new-user>
    <div class="grid grid-2 gap-3">
      <label class="field"><span class="field-label">Email</span>
        <input class="input" type="email" data-u-email required placeholder="name@example.com"></label>
      <label class="field"><span class="field-label">Name</span>
        <input class="input" type="text" data-u-name placeholder="Full name"></label>
      <label class="field"><span class="field-label">Password (min 8)</span>
        <input class="input" type="password" data-u-pass required minlength="8" placeholder="••••••••"></label>
      <label class="field"><span class="field-label">Role</span>
        <select class="select" data-u-role>
          <option value="author" selected>Author</option>
          <option value="editor">Editor</option>
          <option value="admin">Admin</option>
        </select></label>
    </div>
    <div class="vm-row mt-3">
      <button class="btn btn--primary btn--sm" type="submit">Create account</button>
      <span class="muted text-sm" data-team-status></span>
    </div>
  </form>
  <p class="field-hint mt-3">` + mailNote + `</p>
</div>`
}

// =============================================================================
// Public author profile
// =============================================================================

// GET /author/{id} — a public profile page for a staff member.
func (a *App) handlePublicAuthor(w http.ResponseWriter, r *http.Request) {
	if a.userStore == nil {
		http.NotFound(w, r)
		return
	}
	// Resolve by human-readable username first (/author/ankush), then fall back to
	// the opaque id (/author/<hash>) so existing links keep working.
	key := chi.URLParam(r, "id")
	u, err := a.userStore.GetByUsername(r.Context(), key)
	if err != nil || u == nil {
		u, err = a.userStore.GetByID(r.Context(), key)
	}
	if err != nil || u == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "index, follow")

	esc := html.EscapeString
	brand := esc(config.Cfg.Domain)
	name := esc(u.Name)
	if name == "" {
		name = esc(authorFallbackName(u.Email))
	}

	avatar := `<div class="au-avatar au-avatar--placeholder" aria-hidden="true">` + esc(initials(u.Name, u.Email)) + `</div>`
	if u.AvatarURL != "" {
		avatar = `<img class="au-avatar" src="` + esc(u.AvatarURL) + `" alt="` + name + `" width="96" height="96">`
	}

	roleLabel := strings.ToUpper(u.Role[:1]) + u.Role[1:]

	bio := ""
	if u.Bio != "" {
		bio = `<p class="au-bio">` + esc(u.Bio) + `</p>`
	}

	socials := ""
	if len(u.Socials) > 0 {
		keys := make([]string, 0, len(u.Socials))
		for k := range u.Socials {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		links := ""
		for _, k := range keys {
			links += `<a class="au-social" href="` + esc(u.Socials[k]) + `" rel="noopener nofollow" target="_blank">` + esc(socialLabel(k)) + `</a>`
		}
		socials = `<div class="au-socials">` + links + `</div>`
	}

	// The author's recent published posts. VayuPress is single-author per site
	// (posts aren't tagged with an author id), so this lists the site's published
	// posts — read pool + indexed predicate + LIMIT (scale-safe).
	// Posts by this author. The site's primary author owns the (huge) legacy set
	// of posts with no explicit author_id, so for them we use the proven,
	// index-friendly "all published" query. Secondary authors filter on the
	// indexed author_id column (they have few posts) — scale-safe on 234k rows.
	siteAuthor := strings.TrimSpace(render.GetActiveSettings().Author)
	isPrimary := siteAuthor != "" && strings.EqualFold(strings.TrimSpace(u.Name), siteAuthor)
	postsHTML := `<p class="au-posts-empty">No posts published yet.</p>`
	var (
		rows *sql.Rows
		qerr error
	)
	if isPrimary {
		rows, qerr = dbpkg.Reader().QueryContext(r.Context(),
			`SELECT title,slug,created_at,COALESCE(excerpt,'') FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC LIMIT 60`)
	} else {
		rows, qerr = dbpkg.Reader().QueryContext(r.Context(),
			`SELECT title,slug,created_at,COALESCE(excerpt,'') FROM articles WHERE author_id=? AND status='published' AND is_page=0 ORDER BY created_at DESC LIMIT 60`, u.ID)
	}
	if qerr == nil {
		defer rows.Close()
		list := ""
		n := 0
		for rows.Next() {
			var title, slug, created, excerpt string
			if rows.Scan(&title, &slug, &created, &excerpt) != nil {
				continue
			}
			date := created
			if len(date) >= 10 {
				date = date[:10]
			}
			ex := ""
			if excerpt != "" {
				ex = `<p class="au-post-excerpt">` + esc(excerpt) + `</p>`
			}
			list += `<a class="au-post" href="/` + esc(slug) + `">` +
				`<span class="au-post-date">` + esc(date) + `</span>` +
				`<span class="au-post-title">` + esc(title) + `</span>` + ex + `</a>`
			n++
		}
		if n > 0 {
			postsHTML = `<div class="au-posts">` + list + `</div>`
		}
	}

	page := `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + name + ` · ` + brand + `</title>
<meta name="description" content="` + name + ` on ` + brand + `.">
<link rel="stylesheet" href="/theme.css">
<link rel="stylesheet" href="/static/css/signup.css">
<link rel="icon" type="image/png" href="/static/favicon-light.png">
<script src="/static/js/theme-toggle.js" defer></script>
</head>
<body class="su-body au-page">
<button type="button" id="vayu-theme-toggle" class="vayu-theme-toggle au-theme-toggle" aria-label="Toggle theme">☾</button>
<main class="au-wrap" id="main-content">
  <header class="au-hero">
    ` + avatar + `
    <div class="au-hero-info">
      <h1 class="au-name">` + name + `</h1>
      <p class="au-role">` + esc(roleLabel) + `</p>
      ` + bio + socials + `
    </div>
  </header>
  <section class="au-posts-section" aria-label="Posts by ` + name + `">
    <div class="au-posts-head"><span class="au-posts-label">Posts</span></div>
    ` + postsHTML + `
  </section>
  <p class="au-foot"><a class="su-link" href="/">← Back to ` + brand + `</a></p>
</main>
</body></html>`
	_, _ = w.Write([]byte(page))
}

// installAuthorResolver wires render.AuthorInfoFn so the public article byline
// can link to /author/<slug> with the author's avatar. It matches the byline's
// display name (the site author setting) to a staff user by name (case-
// insensitive), returning that user's public handle + avatar. Results are cached
// per name so a busy render path never re-queries; the cache is small (the
// number of distinct author names) and refreshed on restart.
func (a *App) installAuthorResolver() {
	if a.userStore == nil {
		return
	}
	var mu sync.RWMutex
	cache := map[string][2]string{}
	render.AuthorInfoFn = func(name string) (string, string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return "", ""
		}
		mu.RLock()
		if v, ok := cache[key]; ok {
			mu.RUnlock()
			return v[0], v[1]
		}
		mu.RUnlock()
		slug, avatar := "", ""
		if list, err := a.userStore.List(context.Background()); err == nil {
			for i := range list {
				if strings.EqualFold(strings.TrimSpace(list[i].Name), key) {
					slug = authorSlug(&list[i])
					avatar = list[i].AvatarURL
					break
				}
			}
		}
		mu.Lock()
		cache[key] = [2]string{slug, avatar}
		mu.Unlock()
		return slug, avatar
	}

	// Per-post author id → (name, slug, avatar) for multi-author bylines, cached.
	var mu2 sync.RWMutex
	byID := map[string][3]string{}
	render.AuthorByIDFn = func(id string) (string, string, string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return "", "", ""
		}
		mu2.RLock()
		if v, ok := byID[id]; ok {
			mu2.RUnlock()
			return v[0], v[1], v[2]
		}
		mu2.RUnlock()
		name, slug, avatar := "", "", ""
		if u, err := a.userStore.GetByID(context.Background(), id); err == nil && u != nil {
			name = strings.TrimSpace(u.Name)
			if name == "" {
				name = authorFallbackName(u.Email)
			}
			slug = authorSlug(u)
			avatar = u.AvatarURL
		}
		mu2.Lock()
		byID[id] = [3]string{name, slug, avatar}
		mu2.Unlock()
		return name, slug, avatar
	}
}

// authorFallbackName returns a friendly name from an email local part.
func authorFallbackName(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}

// initials derives up-to-two-letter initials from a name (or email local part).
func initials(name, email string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		if i := strings.IndexByte(email, '@'); i > 0 {
			name = email[:i]
		} else {
			name = email
		}
	}
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	out := firstRuneUpper(parts[0])
	if len(parts) > 1 {
		out += firstRuneUpper(parts[len(parts)-1])
	}
	return out
}

// firstRuneUpper returns the first rune of s upper-cased (rune-safe, so it never
// splits a multi-byte character).
func firstRuneUpper(s string) string {
	for _, r := range s {
		return strings.ToUpper(string(r))
	}
	return ""
}
