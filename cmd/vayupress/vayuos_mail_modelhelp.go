// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_modelhelp.go — help from a model in Mail: sum up the
// conversation in front of you, or draft an answer to it from a line. Every
// caution is enforced here, on the server:
//
//   - off until the mailbox's holder turns it on, and only ever for the
//     session's own mailbox, never one an administrator is reading;
//   - a model outside the server only for a mailbox the operator allowed;
//     a model is "on this server" only when its address is the loopback;
//   - never an encrypted or confidential message to a model outside;
//   - addresses, phone numbers and card-like numbers replaced before text
//     leaves for a model outside, and put back in its answer;
//   - only the conversation open, never the mailbox;
//   - every answer a suggestion the holder uses or throws away;
//   - each request counted against the mailbox with where it went, which
//     the operator sees, never the text.

import (
	"context"
	"errors"
	"github.com/johalputt/vayupress/internal/i18n"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/secrets"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// mailModel is the model a mailbox's help would use, where its text would
// go, in words, and whether that is this server.
type mailModel struct {
	b aiassist.Backend
	// where is the English phrase the administrator's count keeps ("on this
	// server", "sent to …"); provider is who outside the server is sent to.
	where, provider string
	local           bool
}

// whereIn is where the model runs, as the buttons say it, in lang.
func (mm mailModel) whereIn(lang string) string {
	if mm.local {
		return i18n.T(lang, "on this server")
	}
	return i18n.T(lang, "sent to {provider}", "provider", mm.provider)
}

// loopbackEndpoint reports an endpoint whose host is this machine.
func loopbackEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// mailModelFor is the model mailbox may use, or false. A model on this
// server comes first; one outside only when the operator allowed it for
// this mailbox.
func (a *App) mailModelFor(ctx context.Context, mailbox string) (mailModel, bool) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return mailModel{}, false
	}
	mh := a.vayuMail.Accounts().ModelHelpFor(ctx, mailbox)
	if !mh.On {
		return mailModel{}, false
	}
	var outside *mailModel
	for _, p := range a.aiAvailableProviders(ctx) {
		b, ok, _ := a.resolveAIBackend(ctx, p.ID, p.DefaultModel)
		if !ok {
			continue
		}
		if b.Kind == aiassist.KindOllama && loopbackEndpoint(b.Endpoint) {
			return mailModel{b: b, where: "on this server", local: true}, true
		}
		if outside == nil {
			label := p.Label
			if p.ID == secrets.ProviderOllama {
				if u, err := url.Parse(b.Endpoint); err == nil {
					label = u.Hostname()
				}
			}
			outside = &mailModel{b: b, where: "sent to " + label, provider: label}
		}
	}
	if outside != nil && mh.Outside {
		return *outside, true
	}
	return mailModel{}, false
}

// personal is what is withheld from a model outside, most specific first:
// an address, then a card-like run of 13 to 19 digits, then a phone number.
var personal = []struct {
	name string
	re   *regexp.Regexp
}{
	{"ADDRESS", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)},
	{"NUMBER", regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)},
	{"PHONE", regexp.MustCompile(`\+?\d[\d ().-]{6,}\d`)},
}

// maskPersonal replaces what personal matches with numbered placeholders
// and gives the way back.
func maskPersonal(text string) (string, map[string]string) {
	back := map[string]string{}
	for _, p := range personal {
		n := 0
		text = p.re.ReplaceAllStringFunc(text, func(m string) string {
			n++
			ph := "[" + p.name + " " + strconv.Itoa(n) + "]"
			back[ph] = m
			return ph
		})
	}
	return text, back
}

// unmaskPersonal puts back what maskPersonal withheld.
func unmaskPersonal(text string, back map[string]string) string {
	for ph, v := range back {
		text = strings.ReplaceAll(text, ph, v)
	}
	return text
}

// mailConfidential is a message marked as not for sharing.
func mailConfidential(raw []byte, subject string) bool {
	head := strings.ToLower(string(raw[:min(len(raw), 16<<10)]))
	return strings.Contains(head, "\nsensitivity: private") || strings.Contains(head, "\nsensitivity: company-confidential") ||
		strings.Contains(strings.ToLower(subject), "confidential")
}

// mailPlainText is a message's words: its text part, or its HTML's text.
func mailPlainText(pm vmail.ParsedMessage) string {
	if strings.TrimSpace(pm.Text) != "" {
		return pm.Text
	}
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(mailHTMLPolicy.Sanitize(pm.HTML)), ctx)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(nodeText(n) + "\n")
	}
	return b.String()
}

// modelHelpBar is the reader's model help for the message open, naming
// where the text goes on each button; "" when none is allowed for it.
func (a *App) modelHelpBar(ctx context.Context, rd vmail.Reader, folder, id string, cur vmail.StoredMessage, raw []byte, subject string) string {
	if rd.Key() == "" || !rd.IsOwner() {
		return ""
	}
	mm, ok := a.mailModelFor(ctx, a.mailboxOf(rd))
	if !ok || (!mm.local && (cur.Encrypted || mailConfidential(raw, subject))) {
		return ""
	}
	lang := rd.Lang()
	post := ` hx-post="/os/vayumail/model" hx-target="#vm-ai-out" hx-swap="innerHTML"`
	vals := func(op string) string { return hxVals("op", op, "user", rd.Key(), "folder", folder, "id", id) }
	return `<div class="mx-ai"><button type="button" class="btn btn--ghost btn--sm"` + post + vals("summary") + `>` + saIcon("sparkle") + esc(i18n.T(lang, "Summarise")) + ` <span class="mx-ai__where">(` + esc(mm.whereIn(lang)) + `)</span></button>` +
		`<form class="mx-ai__draft"` + post + vals("reply") + `><input class="input input--sm" name="line" maxlength="300" placeholder="` + esc(i18n.T(lang, "Draft an answer that says…")) + `" aria-label="` + esc(i18n.T(lang, "What the answer should say")) + `" autocomplete="off">` +
		`<button type="submit" class="btn btn--ghost btn--sm">` + esc(i18n.T(lang, "Draft")) + ` <span class="mx-ai__where">(` + esc(mm.whereIn(lang)) + `)</span></button></form>` +
		`<div id="vm-ai-out" aria-live="polite"></div></div>`
}

// mailboxOf is the address of the mailbox rd reads.
func (a *App) mailboxOf(rd vmail.Reader) string {
	return mailAddrOf(rd.Key(), a.cfgDomain())
}

// handleVayuOSMailModel runs one model-help request for the session's own
// mailbox and answers with the suggestion.
func (a *App) handleVayuOSMailModel(w http.ResponseWriter, r *http.Request) {
	say := func(s string) { writeOSFragment(w, `<p class="mx-ai__note">`+esc(s)+`</p>`) }
	lang := i18n.Lang(r.Context())
	if !a.mailRunning() {
		say(i18n.T(lang, "Mail is not running."))
		return
	}
	rd := a.mailReader(r, r.PostFormValue("user"))
	if rd.Key() == "" || !rd.IsOwner() {
		say(i18n.T(lang, "Model help is only for your own mailbox."))
		return
	}
	mailbox := a.mailboxOf(rd)
	mm, ok := a.mailModelFor(r.Context(), mailbox)
	if !ok {
		say(i18n.T(lang, "Model help is off for this mailbox."))
		return
	}
	out, err := a.runModelHelp(r.Context(), rd, mm, r.PostFormValue("op"), r.PostFormValue("folder"), r.PostFormValue("id"), r.PostFormValue("line"))
	if err != nil {
		say(err.Error())
		return
	}
	writeOSFragment(w, out)
}

// errModelHelp is a refusal or failure the holder is told in these words.
type errModelHelp string

func (e errModelHelp) Error() string { return string(e) }

// runModelHelp does one request with model mm for the message id in folder
// of the mailbox rd reads, and gives the suggestion as the reader shows it.
func (a *App) runModelHelp(ctx context.Context, rd vmail.Reader, mm mailModel, op, folder, id, line string) (string, error) {
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if err != nil {
		return "", errModelHelp(i18n.T(rd.Lang(), "That message is not here."))
	}
	pm := vmail.ParseMessage(raw)
	msgs, _ := a.vayuMail.ListFolder(rd, folder)
	var cur vmail.StoredMessage
	for _, m := range msgs {
		if m.ID == id {
			cur = m
		}
	}
	// Whether it is encrypted is read from the message as stored (the
	// listing's summary), not from the copy decrypted for reading.
	if !mm.local && (cur.Encrypted || mailConfidential(raw, pm.Subject)) {
		return "", errModelHelp(i18n.T(rd.Lang(), "This message is encrypted or marked confidential, so it is not sent to a model outside this server."))
	}
	// The conversation open: up to four earlier messages of its thread, then
	// this one, each cut to what a model needs.
	var conv strings.Builder
	items := a.mailConversation(rd, folder, cur, msgs)
	if len(items) > 4 {
		items = items[len(items)-4:]
	}
	for _, it := range items {
		// The rest of the conversation is held to the same rule as the
		// message open: the owner's reader decrypts an earlier encrypted
		// message, and that text must not leave for a model outside either.
		if !mm.local && it.msg.Encrypted {
			continue
		}
		b, err := a.vayuMail.ReadFolderMessage(rd, it.folder, it.msg.ID)
		if err != nil {
			continue
		}
		p := vmail.ParseMessage(b)
		if !mm.local && mailConfidential(b, p.Subject) {
			continue
		}
		conv.WriteString("From: " + p.From + "\n" + clipRunes(mailPlainText(p), 2000) + "\n\n")
	}
	conv.WriteString("From: " + pm.From + "\nSubject: " + pm.Subject + "\n" + clipRunes(mailPlainText(pm), 6000))
	aop := aiassist.OpMailSummary
	if op == "reply" {
		aop = aiassist.OpMailReply
		conv.WriteString("\n\nWhat to say: " + clipRunes(line, 300))
	}
	text, back := conv.String(), map[string]string{}
	if !mm.local {
		text, back = maskPersonal(text)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, _, err := aiassist.GenerateOpDetail(ctx, nil, mm.b, aop, text)
	a.vayuMail.Accounts().NoteModelSent(ctx, a.mailboxOf(rd), mm.where)
	if err != nil {
		// Only the provider's own words, scrubbed of addresses, are shown:
		// a transport error carries the endpoint, which may be internal.
		var pe *aiassist.ProviderError
		if errors.As(err, &pe) {
			return "", errModelHelp(i18n.T(rd.Lang(), "The model did not answer: {reason}", "reason", pe.Error()))
		}
		return "", errModelHelp(i18n.T(rd.Lang(), "The model could not be reached."))
	}
	out = strings.TrimSpace(unmaskPersonal(out, back))
	var b strings.Builder
	note := i18n.T(rd.Lang(), "A suggestion, on this server. Nothing has been sent or changed.")
	if !mm.local {
		note = i18n.T(rd.Lang(), "A suggestion, sent to {provider}. Nothing has been sent or changed.", "provider", mm.provider)
	}
	b.WriteString(`<div class="mx-ai__out"><p class="mx-ai__note">` + esc(note) + `</p><div class="mx-ai__text">` + esc(out) + `</div>`)
	if aop == aiassist.OpMailReply {
		u := "/os/vayumail/compose?reply=1&user=" + qparam(rd.Key()) + "&folder=" + qparam(folder) + "&id=" + qparam(id) + "&body=" + url.QueryEscape(out)
		b.WriteString(`<a class="btn btn--sm" href="` + esc(u) + `">` + esc(i18n.T(rd.Lang(), "Reply with this")) + `</a>`)
	}
	b.WriteString(`</div>`)
	return b.String(), nil
}

// clipRunes cuts s to n runes.
func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// modelHelpSetting is model help on a mailbox's Settings: for its holder the
// switch and where their text would go; for an administrator the leave to
// use a model outside the server and how many requests went where. "" when
// the install has no model at all.
func (a *App) modelHelpSetting(ctx context.Context, ac vmail.Account, forHolder bool) string {
	providers := a.aiAvailableProviders(ctx)
	if len(providers) == 0 {
		return ""
	}
	mh := a.vayuMail.Accounts().ModelHelpFor(ctx, ac.Email)
	lang := i18n.Lang(ctx)
	box := func(op, label string, on bool) string {
		checked := ""
		if on {
			checked = " checked"
		}
		return autosaveAttrs + ` class="vm-row" hx-post="/os/vayumail/model/settings" hx-target="#vm-mbox-settings" hx-swap="innerHTML">` +
			`<input type="hidden" name="email" value="` + esc(ac.Email) + `"><input type="hidden" name="op" value="` + op + `">` +
			`<label class="field--check"><input type="checkbox" name="on" value="1"` + checked + `> ` + esc(label) + `</label><button class="btn btn--sm" type="submit">` + esc(i18n.T(lang, "Save")) + `</button></form>`
	}
	if forHolder {
		where := i18n.T(lang, "Nothing is sent anywhere until you turn this on.")
		if mm, ok := a.mailModelFor(ctx, ac.Email); ok {
			where = i18n.T(lang, "It runs on this server.")
			if !mm.local {
				where = i18n.T(lang, "Your text is sent to {provider}, with addresses and numbers withheld. Encrypted and confidential messages are never sent.", "provider", mm.provider)
			}
		}
		return box("help", i18n.T(lang, "Offer to summarise a conversation and draft an answer"), mh.On) + `<p class="text-sm muted">` + esc(where) + ` ` + esc(i18n.T(lang, "Every answer is a suggestion you use or throw away.")) + `</p>`
	}
	// The count keeps where the last request went in English (mailModel.where).
	sent := i18n.T(lang, "No request yet.")
	if mh.Sent > 0 {
		sent = i18n.T(lang, "{n, plural, one {# request, the last {where}.} other {# requests, the last {where}.}}", "n", mh.Sent, "where", mh.SentTo)
	}
	holder := i18n.T(lang, "Its holder turns model help on for themselves (now off).")
	if mh.On {
		holder = i18n.T(lang, "Its holder turns model help on for themselves (now on).")
	}
	return box("outside", i18n.T(lang, "May use a model outside this server"), mh.Outside) +
		`<p class="text-sm muted">` + esc(holder) + ` ` + esc(sent) + ` ` + esc(i18n.T(lang, "Only the count is kept, never the text.")) + `</p>`
}

// handleVayuOSMailModelSettings is the holder's switch and the
// administrator's leave. Each is only theirs to set: an administrator does
// not turn a holder's model help on, and a holder does not allow a model
// outside the server.
func (a *App) handleVayuOSMailModelSettings(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	lang := i18n.Lang(r.Context())
	on := r.PostFormValue("on") == "1"
	var err error
	switch r.PostFormValue("op") {
	case "help":
		if !a.isMailboxHolder(r, email) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only the mailbox's holder turns model help on or off."), "")
			return
		}
		err = a.vayuMail.Accounts().SetModelHelp(r.Context(), email, on)
	case "outside":
		if !a.isAdminRequest(r) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(lang, "Only an administrator allows a model outside this server."), "")
			return
		}
		err = a.vayuMail.Accounts().SetModelOutside(r.Context(), email, on)
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-op", i18n.T(lang, "Unknown setting."), "")
		return
	}
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "not-saved", err.Error(), "")
		return
	}
	quietWhenSaved(w, r, nil)
	writeOSFragment(w, a.acctRefresh(r, email))
}
