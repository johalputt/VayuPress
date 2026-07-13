package main

import (
	"context"
	"errors"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// vayuos_mail_accounts.go — the enterprise, HTMX-driven Accounts surface.
//
// Every mailbox is a collapsible card; every inline action (enable/disable,
// role, quota, retention, delete) is an HTMX POST that swaps the whole
// #vm-accounts-list fragment in place, so the page never does a full reload.
// The prompt-driven flows (create, set-password, 2FA enrolment) stay in
// admin-os-mail.js but refresh the same fragment via htmx.ajax instead of
// window.location.reload(). CSP posture is unchanged: no inline styles, the
// page's single <script> carries the nonce, htmx.min.js is self-hosted, and the
// shared inline glue mirrors vp_csrf into X-CSRF-Token on every hx request.

const acctListHx = ` hx-target="#vm-accounts-list" hx-swap="innerHTML"`

// vayuAccountsList renders the stats strip plus one collapsible card per mailbox.
// It is the swap target for every account action and the create flow, so it is
// always re-rendered whole — keeping the storage figures, badges and counts live.
func (a *App) vayuAccountsList(ctx context.Context) string {
	accts := a.vayuMail.Accounts()
	accs, _ := accts.List(ctx)

	active, twofa := 0, 0
	var usedTotal float64
	for _, ac := range accs {
		if ac.Active {
			active++
		}
		if ac.TOTPEnabled {
			twofa++
		}
		usedTotal += float64(a.vayuMail.MailboxUsage(ac.Email)) / (1024 * 1024)
	}
	pending := 0
	for _, d := range accts.ListDevices(ctx) {
		if d.Status == vmail.DeviceStatusPending {
			pending++
		}
	}

	var b strings.Builder
	// Stats strip — an at-a-glance enterprise summary that refreshes on every swap.
	b.WriteString(`<div class="vm-stats">`)
	b.WriteString(vmStatTile(strconv.Itoa(len(accs)), "Mailboxes", ""))
	b.WriteString(vmStatTile(strconv.Itoa(active), "Active", ""))
	b.WriteString(vmStatTile(strconv.Itoa(twofa), "2FA on", ""))
	pendCls := ""
	if pending > 0 {
		pendCls = "warn"
	}
	b.WriteString(vmStatTile(strconv.Itoa(pending), "Devices pending", pendCls))
	b.WriteString(vmStatTile(strconv.FormatFloat(usedTotal, 'f', 1, 64)+" MB", "Storage used", ""))
	b.WriteString(`</div>`)

	if len(accs) == 0 {
		b.WriteString(`<div class="card empty-state">No mail accounts yet. Create one above — it can sign in over SMTP/IMAP/POP3 straight away.</div>`)
		return b.String()
	}

	// Group the mailboxes by domain (VayuDomains): the primary first, then each
	// secondary alphabetically — so domains are managed separately, not mixed into
	// one list. A single-domain install renders the flat list unchanged.
	primary := strings.ToLower(strings.TrimSpace(a.vayuMail.Config().Domain))
	groups := map[string][]vmail.Account{}
	for _, ac := range accs {
		d := emailDomain(ac.Email, primary)
		groups[d] = append(groups[d], ac)
	}
	order := make([]string, 0, len(groups))
	if _, ok := groups[primary]; ok && primary != "" {
		order = append(order, primary)
	}
	others := make([]string, 0, len(groups))
	for d := range groups {
		if d != primary {
			others = append(others, d)
		}
	}
	sort.Strings(others)
	order = append(order, others...)

	if len(order) <= 1 {
		b.WriteString(`<div class="vm-acct-list">`)
		for _, ac := range accs {
			b.WriteString(a.vayuAccountCard(ctx, ac))
		}
		b.WriteString(`</div>`)
		return b.String()
	}
	for _, d := range order {
		list := groups[d]
		role := `<span class="badge badge--muted">secondary</span>`
		if d == primary {
			role = `<span class="badge badge--accent">primary</span>`
		}
		unit := "mailboxes"
		if len(list) == 1 {
			unit = "mailbox"
		}
		b.WriteString(`<div class="vm-acct-domain"><div class="vm-dom-head"><span class="vm-dom-name">` + html.EscapeString(d) + `</span> ` + role +
			` <span class="vm-dom-count">` + strconv.Itoa(len(list)) + ` ` + unit + `</span></div><div class="vm-acct-list">`)
		for _, ac := range list {
			b.WriteString(a.vayuAccountCard(ctx, ac))
		}
		b.WriteString(`</div></div>`)
	}
	return b.String()
}

// vayuCardForwarding renders a mailbox's auto-forward control inside its card.
// It posts op=forward-set to the alias action endpoint and refreshes the whole
// accounts list so the change is reflected everywhere.
func vayuCardForwarding(ac vmail.Account) string {
	he := html.EscapeString(ac.Email)
	return `<form class="vm-row vm-card-fwd" hx-post="/os/vayumail/aliases/action" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="op" value="forward-set"><input type="hidden" name="email" value="` + he + `">` +
		`<input class="input input--sm vm-grow" type="email" name="forward" value="` + html.EscapeString(ac.ForwardTo) + `" placeholder="someone@elsewhere.com — blank to turn off" aria-label="Forward address for ` + he + `">` +
		`<button class="btn btn--sm" type="submit">Save</button></form>`
}

// vayuCardVacation renders a mailbox's vacation autoresponder inside its card (a
// collapsible form), posting to the autoreply endpoint and refreshing the list.
func (a *App) vayuCardVacation(ctx context.Context, ac vmail.Account) string {
	ar := a.vayuMail.Accounts().AutoreplyFor(ctx, ac.Email)
	state := `<span class="muted text-xs">off</span>`
	if ar.Active(time.Now()) {
		state = `<span class="badge badge--ok">active</span>`
	} else if ar.Enabled {
		state = `<span class="badge badge--warn">scheduled</span>`
	}
	checked := ""
	if ar.Enabled {
		checked = " checked"
	}
	fromVal, untilVal := "", ""
	if !ar.From.IsZero() {
		fromVal = ar.From.Local().Format("2006-01-02")
	}
	if !ar.Until.IsZero() {
		untilVal = ar.Until.Local().Format("2006-01-02")
	}
	he := html.EscapeString(ac.Email)
	var b strings.Builder
	b.WriteString(`<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Vacation autoresponder</span> ` + state + `</summary>`)
	b.WriteString(`<form class="vm-ooo-form" hx-post="/os/vayumail/autoreply/action" hx-target="#vm-accounts-list" hx-swap="innerHTML">`)
	b.WriteString(`<input type="hidden" name="email" value="` + he + `">`)
	b.WriteString(`<label class="vm-row text-sm"><input type="checkbox" name="enabled" value="1"` + checked + `> Enabled</label>`)
	b.WriteString(`<div class="vm-row vm-row--end">`)
	b.WriteString(`<label class="field vm-grow"><span class="field-label">Subject</span><input class="input input--sm" type="text" name="subject" value="` + html.EscapeString(ar.Subject) + `" placeholder="Out of office"></label>`)
	b.WriteString(`<label class="field"><span class="field-label">First day (optional)</span><input class="input input--sm" type="date" name="from" value="` + fromVal + `"></label>`)
	b.WriteString(`<label class="field"><span class="field-label">Last day (optional)</span><input class="input input--sm" type="date" name="until" value="` + untilVal + `"></label>`)
	b.WriteString(`</div>`)
	b.WriteString(`<label class="field"><span class="field-label">Message</span><textarea class="input" name="body" rows="3" placeholder="I am away until …">` + html.EscapeString(ar.Body) + `</textarea></label>`)
	b.WriteString(`<button class="btn btn--primary btn--sm" type="submit">Save</button>`)
	b.WriteString(`</form></details>`)
	return b.String()
}

// vmStatTile renders one summary tile. tone "" is neutral, "warn" highlights a
// value that wants operator attention (e.g. devices awaiting approval).
func vmStatTile(value, label, tone string) string {
	cls := "vm-stat"
	if tone != "" {
		cls += " vm-stat--" + tone
	}
	return `<div class="` + cls + `"><span class="vm-stat__v">` + html.EscapeString(value) +
		`</span><span class="vm-stat__l">` + html.EscapeString(label) + `</span></div>`
}

// vayuAccountCard renders one mailbox as a collapsible <details> card: a scannable
// summary (address, role, status, 2FA, storage) with every control revealed on
// expand. Inline controls are HTMX; the prompt-driven ones keep their data-*
// hooks for admin-os-mail.js.
func (a *App) vayuAccountCard(ctx context.Context, ac vmail.Account) string {
	esc := html.EscapeString
	email := esc(ac.Email)
	initial := "?"
	if t := strings.TrimSpace(ac.Email); t != "" {
		initial = strings.ToUpper(t[:1])
	}

	// Badges for the summary line.
	roleName := ac.Role
	if roleName == "" {
		roleName = "mailbox"
	}
	statusBadge := `<span class="badge badge--ok">active</span>`
	if !ac.Active {
		statusBadge = `<span class="badge badge--warn">disabled</span>`
	}
	twofaBadge := `<span class="badge badge--muted">2FA off</span>`
	if ac.TOTPEnabled {
		twofaBadge = `<span class="badge badge--ok">2FA on</span>`
	}

	// Storage: a native <meter> (CSP-safe, no inline style, survives HTMX swaps)
	// when a quota is set; plain used-MB text when unlimited.
	usedMB := float64(a.vayuMail.MailboxUsage(ac.Email)) / (1024 * 1024)
	quotaMB := int64(0)
	if ac.QuotaBytes > 0 {
		quotaMB = ac.QuotaBytes / (1024 * 1024)
	}
	usedStr := strconv.FormatFloat(usedMB, 'f', 1, 64)
	var storageSummary string
	if quotaMB > 0 {
		storageSummary = `<meter class="vm-meter" min="0" max="` + strconv.FormatInt(quotaMB, 10) + `" value="` +
			usedStr + `" title="` + usedStr + ` of ` + strconv.FormatInt(quotaMB, 10) + ` MB"></meter>` +
			`<span class="vm-acct__usage muted text-sm">` + usedStr + `/` + strconv.FormatInt(quotaMB, 10) + ` MB</span>`
	} else {
		storageSummary = `<span class="vm-acct__usage muted text-sm">` + usedStr + ` MB · no limit</span>`
	}

	// Role select (HTMX on change).
	roleSel := `<select class="input input--sm" name="role" hx-post="/os/vayumail/accounts/action"` +
		hxVals("op", "role", "email", ac.Email) + ` hx-trigger="change"` + acctListHx + ` aria-label="Role">`
	for _, rr := range vmail.BuiltinRoles {
		sel := ""
		if strings.EqualFold(rr, ac.Role) {
			sel = " selected"
		}
		roleSel += `<option value="` + rr + `"` + sel + `>` + strings.ToUpper(rr[:1]) + rr[1:] + `</option>`
	}
	if ac.Role != "" && !vmail.IsBuiltinRole(ac.Role) {
		roleSel += `<option value="` + esc(ac.Role) + `" selected>` + esc(ac.Role) + `</option>`
	}
	roleSel += `</select>`

	// Retention select (HTMX on change).
	retDays := a.vayuMail.Accounts().RetentionDays(ctx, ac.Email)
	retSel := `<select class="input input--sm" name="retention_days" hx-post="/os/vayumail/accounts/action"` +
		hxVals("op", "retention", "email", ac.Email) + ` hx-trigger="change"` + acctListHx + ` aria-label="Auto-delete read mail">`
	for _, opt := range []struct {
		Days  int
		Label string
	}{{0, "Off"}, {30, "30 days"}, {90, "90 days"}, {180, "180 days"}, {365, "1 year"}} {
		sel := ""
		if retDays == opt.Days {
			sel = " selected"
		}
		retSel += `<option value="` + strconv.Itoa(opt.Days) + `"` + sel + `>` + opt.Label + `</option>`
	}
	retSel += `</select>`

	// Quota input + Save (HTMX; the button includes the sibling input).
	quotaID := "vm-q-" + esc(ac.Email)
	quotaField := `<input class="input input--sm vm-quota-input" id="` + quotaID + `" type="number" min="0" step="1" name="quota_mb" value="` +
		strconv.FormatInt(quotaMB, 10) + `" aria-label="Quota in MB (0 = unlimited)">` +
		`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/accounts/action"` +
		hxVals("op", "quota", "email", ac.Email) + ` hx-include="#` + quotaID + `"` + acctListHx + `>Save</button>`

	// Enable/disable toggle + delete (HTMX).
	toggleLabel, toggleTo := "Disable", "false"
	if !ac.Active {
		toggleLabel, toggleTo = "Enable", "true"
	}
	toggleBtn := `<button type="button" class="btn btn--sm" hx-post="/os/vayumail/accounts/action"` +
		hxVals("op", "toggle", "email", ac.Email, "active", toggleTo) + acctListHx + `>` + toggleLabel + `</button>`
	deleteBtn := `<button type="button" class="btn btn--sm btn--danger" hx-post="/os/vayumail/accounts/action"` +
		hxVals("op", "delete", "email", ac.Email) + acctListHx +
		` hx-confirm="Delete ` + email + `? Its mailbox and all stored mail are removed permanently.">Delete</button>`

	// Prompt-driven controls stay in admin-os-mail.js (they need a dialog), but the
	// page no longer reloads — their JS refreshes #vm-accounts-list via htmx.ajax.
	passBtn := `<button type="button" class="btn btn--sm" data-acct-pass="` + email + `">Set password</button>`
	twofaBtn := `<button type="button" class="btn btn--sm" data-acct-2fa-enable="` + email + `">Enable 2FA</button>`
	if ac.TOTPEnabled {
		twofaBtn = `<button type="button" class="btn btn--sm" data-acct-2fa-disable="` + email + `">Disable 2FA</button>`
	}

	// Summary avatar: the uploaded profile picture when set, else initials.
	avatar := `<span class="vm-acct__avatar" aria-hidden="true">` + esc(initial) + `</span>`
	hasAvatar := strings.TrimSpace(ac.AvatarType) != ""
	if hasAvatar {
		avatar = `<img class="vm-acct__avatar vm-acct__avatar--img" src="/os/vayumail/accounts/avatar?email=` + qparam(ac.Email) + `" alt="" aria-hidden="true">`
	}

	var c strings.Builder
	c.WriteString(`<details class="vm-acct card">`)
	c.WriteString(`<summary class="vm-acct__sum">`)
	c.WriteString(avatar)
	c.WriteString(`<span class="vm-acct__id"><span class="vm-acct__email mono">` + email + `</span>`)
	if strings.TrimSpace(ac.FullName) != "" {
		c.WriteString(`<span class="vm-acct__name muted text-sm">` + esc(ac.FullName) + `</span>`)
	}
	c.WriteString(`</span>`)
	c.WriteString(`<span class="vm-acct__badges"><span class="badge badge--info">` + esc(roleName) + `</span>` + statusBadge + twofaBadge + `</span>`)
	c.WriteString(`<span class="vm-acct__store">` + storageSummary + `</span>`)
	c.WriteString(`<span class="vm-acct__chev" aria-hidden="true"></span>`)
	c.WriteString(`</summary>`)

	c.WriteString(`<div class="vm-acct__body">`)
	c.WriteString(`<div class="vm-acct__grid">`)
	c.WriteString(`<label class="field"><span class="field-label">Role</span>` + roleSel + `</label>`)
	c.WriteString(`<label class="field"><span class="field-label">Auto-delete read mail</span>` + retSel + `</label>`)
	c.WriteString(`<label class="field"><span class="field-label">Quota (MB, 0 = unlimited)</span><span class="vm-row">` + quotaField + `</span></label>`)
	c.WriteString(`</div>`)
	// Per-mailbox forwarding + vacation live inside the mailbox's own card (all of
	// an address's settings in one place). Both refresh the whole list on save.
	c.WriteString(`<div class="vm-acct__sub"><span class="field-label">Auto-forward a copy to</span>` + vayuCardForwarding(ac) + `</div>`)
	c.WriteString(a.vayuCardVacation(ctx, ac))
	// Profile picture: direct upload (≤500 KB) + optional remove. HTMX multipart,
	// swapping the whole list so the new avatar shows immediately.
	removeBtn := ""
	if hasAvatar {
		removeBtn = `<button class="btn btn--sm btn--ghost" type="button" hx-post="/os/vayumail/accounts/avatar/remove" ` + hxVals("email", ac.Email) + ` hx-target="#vm-accounts-list" hx-swap="innerHTML">Remove picture</button>`
	}
	c.WriteString(`<div class="vm-acct__avatar-edit"><span class="field-label">Profile picture</span>` +
		`<form class="vm-row vm-avatar-form" hx-post="/os/vayumail/accounts/avatar" hx-encoding="multipart/form-data" hx-target="#vm-accounts-list" hx-swap="innerHTML">` +
		`<input type="hidden" name="email" value="` + email + `">` +
		`<input class="input input--sm" type="file" name="avatar" accept="image/png,image/jpeg,image/gif,image/webp" aria-label="Profile picture">` +
		`<button class="btn btn--sm" type="submit">Upload</button></form>` + removeBtn +
		`<span class="text-xs muted">PNG, JPEG, GIF or WebP · up to 500 KB.</span></div>`)
	c.WriteString(`<div class="vm-acct__meta muted text-sm">Created ` + ac.CreatedAt.Format("2006-01-02") + `</div>`)
	c.WriteString(`<div class="vm-acct__actions">` + passBtn + twofaBtn + toggleBtn + deleteBtn + `</div>`)
	c.WriteString(`</div>`)
	c.WriteString(`</details>`)
	return c.String()
}

// handleVayuOSAccountsFragment returns the accounts list fragment (HTMX swap
// target). Admin-only; used by the create/2FA/set-password JS flows to refresh
// the list without a full page reload.
func (a *App) handleVayuOSAccountsFragment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	writeOSHTML(w, a.vayuAccountsList(r.Context()))
}

// handleVayuOSAccountsAction applies one inline account action (toggle / role /
// quota / retention / delete) and returns the refreshed list fragment (HTMX
// swap). Admin-only, CSRF-protected. The prompt-driven mutations (password, 2FA,
// create) keep their dedicated JSON endpoints.
func (a *App) handleVayuOSAccountsAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	_ = r.ParseForm()
	accts := a.vayuMail.Accounts()
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	op := r.FormValue("op")
	var opErr error
	switch op {
	case "toggle":
		active := r.FormValue("active") == "true"
		if opErr = accts.SetActive(r.Context(), email, active); opErr == nil {
			state := "disabled"
			if active {
				state = "enabled"
			}
			dbpkg.AuditLog("vayumail.account.active", dbpkg.AuditActor(r), email, state)
		}
	case "role":
		role := strings.TrimSpace(r.FormValue("role"))
		if opErr = accts.SetRole(r.Context(), email, role); opErr == nil {
			dbpkg.AuditLog("vayumail.account.role", dbpkg.AuditActor(r), email, role)
		}
	case "quota":
		mb, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("quota_mb")), 64)
		bytes := int64(mb * 1024 * 1024)
		if bytes < 0 {
			bytes = 0
		}
		opErr = accts.SetQuota(r.Context(), email, bytes)
	case "retention":
		days, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("retention_days")))
		if opErr = accts.SetRetentionDays(r.Context(), email, days); opErr == nil {
			dbpkg.AuditLog("vayumail.retention.set", dbpkg.AuditActor(r), email, strconv.Itoa(days)+" days")
		}
	case "delete":
		if opErr = accts.Delete(r.Context(), email); opErr == nil {
			dbpkg.AuditLog("vayumail.account.delete", dbpkg.AuditActor(r), email, "")
		}
	default:
		opErr = errors.New("unknown operation")
	}

	list := a.vayuAccountsList(r.Context())
	if opErr != nil {
		list = `<div class="empty-state" role="alert">⚠ ` + html.EscapeString(opErr.Error()) + `</div>` + list
	}
	writeOSHTML(w, list)
}

// handleVayuOSDevicesFragment returns the Devices card fragment. It backs the
// poller inside #vm-device-card so a newly-registered pending device surfaces
// within seconds — no full-page refresh (VayuMail Accounts redesign). Admin-only.
func (a *App) handleVayuOSDevicesFragment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	writeOSHTML(w, a.vayuDevicesCard(r.Context()))
}
