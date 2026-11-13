// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/johalputt/vayupress/internal/i18n"
	htmpl "html/template"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/render"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
)

// The compose sheet (Mail plan §5, render 03). One form, served two ways: in
// Mail it is fetched into the reader column (handleVayuOSComposeSheet), so the
// message being answered stays one click away; /os/vayumail/compose renders the
// same sheet as the page, for a deep link or a browser without JavaScript.

// handleVayuOSCompose is the compose page: the sheet, full width.
func (a *App) handleVayuOSCompose(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if !a.mailRunning() {
		a.writeMailSetup(w, r, "Compose")
		return
	}
	sheet, refusal := a.composeSheet(r, true)
	if refusal != "" {
		sheet = `<div class="empty-state">` + refusal + `</div>`
	} else {
		sheet += `<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`
	}
	writeOSHTML(w, r, adminOSLayout(nonce, "Compose", "vayuos", cfg, htmpl.HTML(sheet)))
}

// handleVayuOSComposeSheet is the same sheet as a fragment, for Mail to open
// over the reader. It takes the compose page's parameters.
func (a *App) handleVayuOSComposeSheet(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeOSFragment(w, "")
		return
	}
	sheet, refusal := a.composeSheet(r, false)
	if refusal != "" {
		sheet = `<div class="mx-compose"><p class="mx-empty">` + refusal + `</p></div>`
	}
	writeOSFragment(w, sheet)
}

// handleVayuOSComposeRecipient tells the sheet about one recipient: whether a
// message to them can be encrypted, and the initials and tone of their avatar.
// The key is resolved exactly as the send resolves it (vpgp CanEncryptTo: the
// key on file, else one the sending mailbox holds for them, else the
// recipient's own WKD, never in a Tor Space), so the lock on a chip and the
// line beside Send say what the send will do.
func (a *App) handleVayuOSComposeRecipient(w http.ResponseWriter, r *http.Request) {
	addr := strings.TrimSpace(r.URL.Query().Get("addr"))
	_, email := mailParseFrom(addr)
	if email == "" {
		email = addr
	}
	var known vpgp.KnownKeys
	if a.mailRunning() {
		known = a.vayuMail.KnownKeys(a.mailReader(r, mailUserParam(r)))
	}
	key := a.vayuPGP != nil && strings.Contains(email, "@") && a.vayuPGP.CanEncryptTo(email, known)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"key":      key,
		"initials": mailInitials(addr),
		"tone":     mailAvatarIdx(strings.ToLower(email)),
	})
}

// composeSheet renders the sheet, or says why this request may not compose.
// page is the full-width page, which has no minimise or expand and closes back
// to the mailbox.
func (a *App) composeSheet(r *http.Request, page bool) (sheet, refusal string) {
	lang := i18n.Lang(r.Context())
	if !a.isAdminRequest(r) {
		if _, own := a.ownMailbox(r); own != "" && a.vayuMail.MailboxReadOnly(own) {
			return "", esc(i18n.T(lang, "This mailbox is read-only. It can read mail here, but not send it."))
		}
	}
	domain := a.vayuMail.Config().Domain
	acctStore := a.vayuMail.Accounts()
	// The mailbox the composer was opened from is the sender. Every Compose, Reply
	// and Forward link carries ?user=, and without this an administrator working
	// in alice's mailbox answered her correspondents from postmaster — the first
	// option — which is a different person as far as the recipient can tell.
	viewing := ""
	if u := mailUserParam(r); u != "" {
		viewing = mailAddrOf(u, domain)
	}
	// Each From option carries its account's signature (data-sig) so the composer
	// can preview/append it and swap it live when the sender changes, and shows
	// the account's name before the whole address. It also says whether the
	// account has an OpenPGP key to sign with (data-can-sign) and whether it
	// signs unless told otherwise (data-sign).
	optSig := func(email, name, sig string) string {
		sel := ""
		if viewing != "" && strings.EqualFold(email, viewing) {
			sel = " selected"
		}
		label := email
		if name != "" {
			label = name + " " + email
		}
		signing := ""
		if a.vayuPGP != nil {
			if _, err := a.vayuPGP.GetPublicKey(email); err == nil {
				signing = ` data-can-sign="1"`
				if acctStore != nil && acctStore.SignsByDefault(r.Context(), email) {
					signing += ` data-sign="1"`
				}
			}
		}
		return `<option value="` + esc(email) + `" data-sig="` + esc(sig) + `"` + signing + sel + `>` + esc(label) + `</option>`
	}
	fromOpts := ""
	if a.isAdminRequest(r) {
		pm := "postmaster@" + domain
		pmSig := ""
		if acctStore != nil {
			pmSig = acctStore.SignatureFor(r.Context(), pm)
		}
		// Group the sender identities by domain (VayuDomains), the primary first
		// (it holds postmaster). A single-domain install has no optgroup chrome.
		domOf := func(email string) string {
			if i := strings.LastIndexByte(email, '@'); i >= 0 && i < len(email)-1 {
				return strings.ToLower(email[i+1:])
			}
			return domain
		}
		byDom := map[string]string{domain: optSig(pm, "", pmSig)}
		order := []string{domain}
		if acctStore != nil {
			if accs, err := acctStore.List(r.Context()); err == nil {
				for _, ac := range accs {
					d := domOf(ac.Email)
					if _, seen := byDom[d]; !seen {
						order = append(order, d)
					}
					byDom[d] += optSig(ac.Email, ac.FullName, ac.Signature)
				}
			}
		}
		if len(order) == 1 {
			fromOpts = byDom[domain]
		} else {
			for _, d := range order {
				fromOpts += `<optgroup label="` + esc(d) + `">` + byDom[d] + `</optgroup>`
			}
		}
	} else {
		_, ownEmail := a.ownMailbox(r)
		if ownEmail == "" {
			return "", esc(i18n.T(lang, "No mailbox has been assigned to your account yet. Ask an administrator to assign you an email address under Members → Team & roles."))
		}
		ownSig, ownName := "", ""
		if acctStore != nil {
			ownSig, ownName = acctStore.SignatureFor(r.Context(), ownEmail), acctStore.FullNameFor(r.Context(), ownEmail)
		}
		fromOpts = optSig(ownEmail, ownName, ownSig)
	}

	prefillTo, prefillCc, prefillBcc, prefillSubject, prefillBody, quote := a.composePrefill(r)

	// Reopening a saved draft: show the files it is holding and carry its id, so
	// pressing Send merges them instead of quietly sending a message without the
	// attachments the sender put on it. Files cannot be re-materialised as file
	// inputs, so the server keeps them and the send path picks them up by id.
	draftID := strings.TrimSpace(r.URL.Query().Get("id"))
	draftFiles := ""
	if r.URL.Query().Get("draft") != "" && draftID != "" {
		draftFiles = `<input type="hidden" data-c-draft-id value="` + esc(draftID) + `">`
		if rd := a.mailReader(r, mailUserParam(r)); rd.Key() != "" {
			if files, derr := a.vayuMail.DraftAttachments(rd, draftID); derr == nil && len(files) > 0 {
				var fb strings.Builder
				fb.WriteString(`<div class="mx-compose__saved"><span>` + saIcon("clip") + ` ` + esc(i18n.T(lang, "Saved with this draft, and sent with it:")) + `</span><span class="vm-attach-list">`)
				for _, f := range files {
					fb.WriteString(`<span class="vm-attach-chip"><span class="vm-attach-ico" aria-hidden="true">` + saIcon("doc") + `</span>` +
						`<span class="vm-attach-name">` + esc(f.Filename) + `</span>` +
						`<span class="vm-attach-size">` + esc(humanBytes(int64(len(f.Data)))) + `</span></span>`)
				}
				fb.WriteString(`</span></div>`)
				draftFiles += fb.String()
			}
		}
	}

	// Feedback mode (the account menu's "Send feedback" links to ?feedback=1):
	// address the feedback inbox and drop in a structured template. Any
	// explicit ?to/subject/body still wins, so the mode only fills the blanks.
	feedback := r.URL.Query().Get("feedback") == "1"
	if feedback {
		if prefillTo == "" {
			prefillTo = a.feedbackEmail(r.Context())
		}
		if prefillSubject == "" {
			prefillSubject = feedbackSubject
		}
		if prefillBody == "" {
			prefillBody = feedbackBody()
		}
	}

	// Recipient autocomplete is scoped to the SENDING mailbox's own address book
	// only (never a shared/global directory), so one mailbox's contacts stay
	// private to it. The owner is the opened mailbox; an admin composing without a
	// specific mailbox falls back to postmaster's book.
	composeOwner := ""
	if o, ok := a.contactOwner(r, mailUserParam(r)); ok {
		composeOwner = o
	} else if a.isAdminRequest(r) {
		composeOwner = "postmaster@" + domain
	}

	title := prefillSubject
	if strings.TrimSpace(title) == "" {
		title = i18n.T(lang, "New message")
	}
	back := "/os/vayumail/inbox"
	if u := mailUserParam(r); u != "" {
		back += "?user=" + qparam(u)
	}
	var b strings.Builder
	cls := "mx-compose"
	if page {
		cls += " mx-compose--page"
	}
	// On its own page the sheet is the page: it carries the page's kind and
	// its title is the page's h1. Over the reader it is an h2 under Mail's.
	kind, h := "", "h2"
	if page {
		kind, h = ` data-page-kind="app"`, "h1"
	}
	b.WriteString(`<form class="` + cls + `" data-mail-compose` + kind + ` data-c-user="` + esc(mailUserParam(r)) + `" aria-labelledby="mx-compose-title">`)
	b.WriteString(`<div class="mx-compose__head"><` + h + ` class="mx-compose__title" id="mx-compose-title" data-c-title>` + esc(title) + `</` + h + `>`)
	if page {
		b.WriteString(`<a class="mx-compose__tool" href="` + esc(back) + `" aria-label="` + esc(i18n.T(lang, "Close")) + `">` + saIcon("x") + `</a>`)
	} else {
		b.WriteString(`<button type="button" class="mx-compose__tool" data-c-min aria-label="` + esc(i18n.T(lang, "Minimise")) + `" aria-keyshortcuts="Escape">` + saIcon("minus") + `</button>` +
			`<button type="button" class="mx-compose__tool" data-c-max aria-label="` + esc(i18n.T(lang, "Expand")) + `" aria-pressed="false">` + saIcon("expand") + `</button>` +
			`<button type="button" class="mx-compose__tool" data-c-close aria-label="` + esc(i18n.T(lang, "Close")) + `">` + saIcon("x") + `</button>`)
	}
	b.WriteString(`</div>`)
	if feedback {
		b.WriteString(`<p class="mx-compose__note">` + saIcon("info") + ` ` + esc(i18n.T(lang, "Tell us about a bug, an improvement or a feature you would like; screenshots and files help.")) + `</p>`)
	}
	b.WriteString(a.composeContactsDatalistFor(r.Context(), composeOwner))
	field := func(label, inner string, attrs string) {
		b.WriteString(`<div class="mx-compose__field"` + attrs + `><span class="mx-compose__label">` + esc(label) + `</span>` + inner + `</div>`)
	}
	chips := func(name, label string) string {
		return `<div class="vm-chips mx-compose__chips" data-c-chips="` + name + `"><input type="text" class="vm-chip-input" data-c-chip-input list="vm-contacts" autocomplete="off" aria-label="` + esc(label) + `"></div>`
	}
	field(i18n.T(lang, "From"), `<select class="mx-compose__from" data-c-from aria-label="`+esc(i18n.T(lang, "From"))+`">`+fromOpts+`</select>`, "")
	field(i18n.T(lang, "To"), chips("to", i18n.T(lang, "To"))+`<button type="button" class="mx-compose__cc" data-c-toggle-cc>`+esc(i18n.T(lang, "Cc Bcc"))+`</button>`, "")
	b.WriteString(`<input type="hidden" data-c-to value="` + esc(prefillTo) + `">`)
	field(i18n.T(lang, "Cc"), chips("cc", i18n.T(lang, "Cc")), ` data-c-cc-field hidden`)
	b.WriteString(`<input type="hidden" data-c-cc value="` + esc(prefillCc) + `">`)
	field(i18n.T(lang, "Bcc"), chips("bcc", i18n.T(lang, "Bcc")), ` data-c-bcc-field hidden`)
	b.WriteString(`<input type="hidden" data-c-bcc value="` + esc(prefillBcc) + `">`)
	field(i18n.T(lang, "Reply-To"), `<input class="mx-compose__input" type="text" data-c-reply aria-label="`+esc(i18n.T(lang, "Reply-To"))+`">`, ` data-c-reply-field hidden`)
	field(i18n.T(lang, "Subject"), `<input class="mx-compose__input" type="text" data-c-subject aria-label="`+esc(i18n.T(lang, "Subject"))+`" value="`+esc(prefillSubject)+`">`, "")

	b.WriteString(`<div class="mx-compose__body">`)
	// Formatting inserts plain-text conventions, because a message body IS plain
	// text end to end (mail.ComposeMessage.Body). A contenteditable WYSIWYG would
	// imply an HTML alternative part the engine does not build, so it would
	// promise formatting the recipient never receives. What goes in here is
	// exactly what is sent, and it stays readable in every client.
	fmtBtn := func(fmt, title, label, face string) string {
		return `<button class="vm-ed-btn" type="button" data-c-fmt="` + fmt + `" title="` + esc(title) + `" aria-label="` + esc(label) + `">` + face + `</button>`
	}
	sep := `<span class="vm-ed-sep" aria-hidden="true"></span>`
	b.WriteString(`<div class="vm-ed-bar" role="toolbar" aria-label="` + esc(i18n.T(lang, "Formatting")) + `" data-c-toolbar hidden>` +
		fmtBtn("bold", i18n.T(lang, "Bold (Ctrl+B)"), i18n.T(lang, "Bold"), "<strong>B</strong>") + fmtBtn("italic", i18n.T(lang, "Italic (Ctrl+I)"), i18n.T(lang, "Italic"), "<em>I</em>") +
		fmtBtn("strike", i18n.T(lang, "Strikethrough"), i18n.T(lang, "Strikethrough"), "<s>S</s>") + sep +
		fmtBtn("h2", i18n.T(lang, "Heading"), i18n.T(lang, "Heading"), "H") + fmtBtn("ul", i18n.T(lang, "Bulleted list"), i18n.T(lang, "Bulleted list"), "&bull;&nbsp;&#8801;") +
		fmtBtn("ol", i18n.T(lang, "Numbered list"), i18n.T(lang, "Numbered list"), "1.&nbsp;&#8801;") + fmtBtn("quote", i18n.T(lang, "Quote"), i18n.T(lang, "Quote"), "&rdquo;") + sep +
		fmtBtn("code", i18n.T(lang, "Code block"), i18n.T(lang, "Code block"), "&lt;/&gt;") + fmtBtn("link", i18n.T(lang, "Link (Ctrl+K)"), i18n.T(lang, "Insert link"), saIcon("link")) +
		fmtBtn("rule", i18n.T(lang, "Divider"), i18n.T(lang, "Divider"), "&mdash;") +
		`<span class="vm-ed-spacer"></span><span class="mx-compose__count" data-c-count aria-live="polite"></span>` +
		`<button class="vm-ed-btn vm-ed-btn--wide" type="button" data-c-preview aria-pressed="false">` + esc(i18n.T(lang, "Preview")) + `</button></div>`)
	b.WriteString(`<textarea class="mx-compose__text" data-c-body placeholder="` + esc(i18n.T(lang, "Write your message…")) + `" aria-label="` + esc(i18n.T(lang, "Message")) + `">` + esc(prefillBody) + `</textarea>`)
	b.WriteString(`<div class="vm-ed-preview" data-c-preview-pane hidden aria-live="polite"></div>`)
	if quote != "" {
		b.WriteString(`<details class="mx-quoted" data-c-quoted><summary>` + saIcon("more") + esc(i18n.T(lang, "Show the quoted message")) + `</summary>` +
			`<pre class="mx-quoted__text" data-c-quote>` + esc(quote) + `</pre>` +
			`<button type="button" class="mx-quoted__drop" data-c-quote-drop>` + esc(i18n.T(lang, "Leave it out")) + `</button></details>`)
	}
	b.WriteString(`<pre class="mx-compose__sig" data-c-sig-preview></pre>`)
	b.WriteString(`<div class="vm-attach-tray" data-c-attach-list></div>` + draftFiles)
	b.WriteString(`</div>`)

	// The foot: Send, the one filled button; Attach, Format and the rest of the
	// options; the line that says whether the message goes encrypted; the
	// draft's state; Discard at the far right.
	b.WriteString(`<div class="mx-compose__foot">`)
	b.WriteString(`<button class="btn btn--primary mx-compose__send" type="submit" data-c-send aria-keyshortcuts="Meta+Enter Control+Enter">` + saIcon("send") + esc(i18n.T(lang, "Send")) + `</button>`)
	// Send later: times the composer fills in from the sender's own clock
	// (the server does not know their zone), or one they pick.
	b.WriteString(`<details class="sa-pop mx-compose__later" data-c-later><summary class="mx-compose__tool" aria-label="` + esc(i18n.T(lang, "Send later")) + `" title="` + esc(i18n.T(lang, "Send later")) + `">` + saIcon("calendar") + `</summary><div class="sa-pop__panel">` +
		`<button type="button" class="mx-compose__opt" data-c-later-preset="morning"></button>` +
		`<button type="button" class="mx-compose__opt" data-c-later-preset="afternoon"></button>` +
		`<button type="button" class="mx-compose__opt" data-c-later-preset="monday"></button>` +
		`<label class="mx-compose__opt mx-compose__when">` + esc(i18n.T(lang, "Pick a time")) + `<input type="datetime-local" class="mx-compose__input" data-c-later-at></label>` +
		`<button type="button" class="btn btn--sm" data-c-later-go>` + esc(i18n.T(lang, "Schedule")) + `</button>` +
		`</div></details>`)
	b.WriteString(`<button type="button" class="mx-compose__tool" data-c-attach-btn aria-label="` + esc(i18n.T(lang, "Attach files")) + `" title="` + esc(i18n.T(lang, "Up to {n} MB in all", "n", composeMaxAttachMB())) + `">` + saIcon("clip") + `</button>`)
	b.WriteString(`<input type="file" data-c-files multiple hidden>`)
	// From Drive: the holder's own files, read as the menu opens
	// (handleVayuOSDrivePick). Not for an administrator writing as another
	// mailbox: Drive is its holder's.
	if _, own := a.driveOwner(r); own {
		b.WriteString(`<details class="sa-pop mx-compose__tpl" data-c-drive hx-get="/os/vayumail/drive/pick" hx-trigger="toggle once" hx-target="find [data-c-drive-list]" hx-swap="outerHTML">` +
			`<summary class="mx-compose__tool" aria-label="` + esc(i18n.T(lang, "Attach from Drive")) + `" title="` + esc(i18n.T(lang, "Attach from Drive")) + `">` + saIcon("folder") + `</summary><div class="sa-pop__panel">` +
			`<ul class="mx-compose__tpls" data-c-drive-list><li class="mx-compose__tpl-empty">` + esc(i18n.T(lang, "Reading your files…")) + `</li></ul></div></details>`)
	}
	b.WriteString(`<button type="button" class="mx-compose__tool" data-c-format aria-label="` + esc(i18n.T(lang, "Format")) + `" aria-pressed="false">` + saIcon("type") + `</button>`)
	// Templates: the mailbox's saved wording, read as the menu opens
	// (vayuos_mail_templates.go).
	b.WriteString(`<details class="sa-pop mx-compose__tpl" data-c-tpl><summary class="mx-compose__tool" aria-label="` + esc(i18n.T(lang, "Templates")) + `" title="` + esc(i18n.T(lang, "Templates")) + `">` + saIcon("doc") + `</summary><div class="sa-pop__panel">` +
		`<ul class="mx-compose__tpls" data-c-tpl-list aria-label="` + esc(i18n.T(lang, "Templates")) + `"><li class="mx-compose__tpl-empty">` + esc(i18n.T(lang, "Reading your templates…")) + `</li></ul>` +
		`<div class="mx-compose__tpl-save"><input class="mx-compose__input" type="text" data-c-tpl-name placeholder="` + esc(i18n.T(lang, "A name for this message")) + `" aria-label="` + esc(i18n.T(lang, "Template name")) + `" maxlength="60">` +
		`<button type="button" class="btn btn--sm" data-c-tpl-save>` + esc(i18n.T(lang, "Save as a template")) + `</button></div>` +
		`<p class="mx-compose__tpl-note" data-c-tpl-note aria-live="polite"></p></div></details>`)
	b.WriteString(`<details class="sa-pop mx-compose__more"><summary class="mx-compose__tool" aria-label="` + esc(i18n.T(lang, "More options")) + `">` + saIcon("more") + `</summary><div class="sa-pop__panel">` +
		`<button type="button" class="mx-compose__opt" data-c-toggle-reply>` + esc(i18n.T(lang, "Set a Reply-To address")) + `</button>` +
		`<label class="mx-compose__opt"><input type="checkbox" data-c-sig-toggle checked> ` + esc(i18n.T(lang, "Append the signature")) + `</label>` +
		`<label class="mx-compose__opt"><input type="checkbox" data-c-rich> ` + esc(i18n.T(lang, "Also send an HTML version")) + `</label>` +
		`<label class="mx-compose__opt" data-c-sign-opt><input type="checkbox" data-c-sign> ` + esc(i18n.T(lang, "Sign with my key")) + `</label>` +
		`<label class="mx-compose__opt" data-c-sign-opt><input type="checkbox" data-c-sign-default> ` + esc(i18n.T(lang, "Sign every message from this address")) + `</label>` +
		`<details class="mx-compose__sigedit"><summary class="mx-compose__opt">` + esc(i18n.T(lang, "Edit the signature")) + `</summary>` +
		`<textarea class="mx-compose__input" rows="4" data-c-sig-text aria-label="` + esc(i18n.T(lang, "Signature")) + `"></textarea>` +
		`<div class="mx-compose__sigbar"><button class="btn btn--sm" type="button" data-c-sig-save>` + esc(i18n.T(lang, "Save signature")) + `</button><span data-c-sig-status></span></div></details>` +
		`</div></details>`)
	// Encryption is on unless the sender turns it off, and the send encrypts only
	// when every recipient has a key; the line beside it says which will happen.
	b.WriteString(`<input type="checkbox" data-c-encrypt checked hidden>`)
	b.WriteString(`<button type="button" class="mx-compose__enc" data-c-enc hidden></button>`)
	b.WriteString(`<span class="mx-compose__status" data-c-status aria-live="polite"></span>`)
	b.WriteString(`<button type="button" class="mx-compose__tool" data-c-discard aria-label="` + esc(i18n.T(lang, "Discard")) + `">` + saIcon("trash") + `</button>`)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="vm-undobar" data-c-undobar hidden role="status" aria-live="polite"><span data-c-undo-text>` + esc(i18n.T(lang, "Sending…")) + `</span><button class="btn btn--sm" type="button" data-c-undo>` + esc(i18n.T(lang, "Undo")) + `</button></div>`)
	b.WriteString(`</form>`)
	return b.String(), ""
}
