// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_rules.go — a mailbox's rules, in Mail's settings.
//
// The rules themselves (conditions, actions, order, the forward that waits
// for its address to confirm) live in VayuMail's filters.go; this is their
// page: each rule said in words, switched on and off and moved in its order,
// one editor for a new rule or an existing one, a test against recent mail
// before saving, and running a saved rule over the mail already there.
//
// The same card serves the holder's own settings and an administrator's page
// for a mailbox, so every write goes through mayChangeMailboxSettings, and
// the test and the run read mail through mailReader, the one decision about
// who may read a mailbox: an administrator testing a rule on a private
// mailbox is refused like any other read.

import (
	"context"
	"errors"
	"github.com/johalputt/vayupress/internal/i18n"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// ruleEditorRows is how many conditions and actions the editor offers; a
// blank row is left out.
const ruleEditorRows = 3

// ruleLookBack is how much recent mail a test, or a run, reads.
const ruleLookBack = 500

func ruleFieldNames(lang string) [][2]string {
	return [][2]string{
		{"from", i18n.T(lang, "From")}, {"to", i18n.T(lang, "To or Cc")}, {"subject", i18n.T(lang, "Subject")}, {"body", i18n.T(lang, "Body")},
		{"domain", i18n.T(lang, "Sender's domain")}, {"attachment", i18n.T(lang, "Has an attachment")}, {"size", i18n.T(lang, "Larger than (MB)")},
		{"list", i18n.T(lang, "From a mailing list")}, {"header", i18n.T(lang, "Header")},
	}
}

// ruleFieldInSentence is what a condition reads, named as it is inside a
// sentence ("When subject contains …"), which a language may write
// differently from the menu's label ("Subject").
func ruleFieldInSentence(lang, field string) string {
	switch field {
	case "from":
		return i18n.T(lang, "from")
	case "to":
		return i18n.T(lang, "to or cc")
	case "subject":
		return i18n.T(lang, "subject")
	case "body":
		return i18n.T(lang, "body")
	case "domain":
		return i18n.T(lang, "sender's domain")
	}
	return field
}

// ruleOpNames are the tests a condition can make, in lang.
func ruleOpNames(lang string) [][2]string {
	return [][2]string{{"contains", i18n.T(lang, "contains")}, {"not-contains", i18n.T(lang, "does not contain")}, {"is", i18n.T(lang, "is")}, {"ends-with", i18n.T(lang, "ends with")}}
}

// ruleActionNames are what a rule can do, in lang.
func ruleActionNames(lang string) [][2]string {
	return [][2]string{
		{"move", i18n.T(lang, "Move to")}, {"label", i18n.T(lang, "Label")}, {"markread", i18n.T(lang, "Mark read")}, {"pin", i18n.T(lang, "Pin")},
		{"forward", i18n.T(lang, "Forward to")}, {"reject", i18n.T(lang, "Reject")}, {"delete", i18n.T(lang, "Delete")},
	}
}

// ruleWords says a rule as the list shows it.
func ruleWords(lang string, r vmail.FilterRule) (when, then string) {
	m0, m1 := htmlMark(0), htmlMark(1)
	// test is one condition: what is read, the test, the words, as one
	// sentence of the language, the two values in bold.
	test := func(field, op, value string) string {
		switch op {
		case "not-contains":
			return escBold(i18n.T(lang, "{field} does not contain {value}", "field", m0, "value", m1), field, value)
		case "is":
			return escBold(i18n.T(lang, "{field} is {value}", "field", m0, "value", m1), field, value)
		case "ends-with":
			return escBold(i18n.T(lang, "{field} ends with {value}", "field", m0, "value", m1), field, value)
		}
		return escBold(i18n.T(lang, "{field} contains {value}", "field", m0, "value", m1), field, value)
	}
	var conds []string
	for _, c := range r.Conditions {
		switch c.Field {
		case "attachment":
			conds = append(conds, "<b>"+esc(i18n.T(lang, "has an attachment"))+"</b>")
		case "size":
			conds = append(conds, escBold(i18n.T(lang, "larger than {size} MB", "size", m0), c.Value))
		case "list":
			if c.Op == "any" {
				conds = append(conds, "<b>"+esc(i18n.T(lang, "from a mailing list"))+"</b>")
				break
			}
			conds = append(conds, test(i18n.T(lang, "mailing list"), c.Op, c.Value))
		case "header":
			conds = append(conds, test(c.Name, c.Op, c.Value))
		default:
			conds = append(conds, test(ruleFieldInSentence(lang, c.Field), c.Op, c.Value))
		}
	}
	joined := ""
	for k, c := range conds {
		switch {
		case k == 0:
			joined = c
		case r.Any:
			joined = escFill(i18n.T(lang, "{first} or {next}", "first", m0, "next", m1), joined, c)
		default:
			joined = escFill(i18n.T(lang, "{first} and {next}", "first", m0, "next", m1), joined, c)
		}
	}
	when = escFill(i18n.T(lang, "When {conditions}", "conditions", m0), joined)
	for k, a := range r.Actions {
		var act string
		switch a.Kind {
		case "move":
			act = escBold(i18n.T(lang, "move to {folder}", "folder", m0), mailFolderLabel(lang, a.Arg))
		case "label":
			act = escBold(i18n.T(lang, "label {label}", "label", m0), a.Arg)
		case "forward":
			act = escBold(i18n.T(lang, "forward to {address}", "address", m0), a.Arg)
		case "reject":
			act = escBold(i18n.T(lang, "{reject}: the sender is told it was refused, and nothing is kept", "reject", m0), i18n.T(lang, "reject"))
		case "markread":
			act = esc(i18n.T(lang, "mark read"))
		case "pin":
			act = esc(i18n.T(lang, "pin"))
		case "delete":
			act = esc(i18n.T(lang, "delete"))
		default:
			act = esc(a.Kind)
		}
		if k == 0 {
			then = act
		} else {
			then = escFill(i18n.T(lang, "{first}, {next}", "first", m0, "next", m1), then, act)
		}
	}
	if r.KeepGoing {
		then = escFill(i18n.T(lang, "{actions}, and keep going", "actions", m0), then)
	}
	return when, then
}

// ruleDomID is a mailbox's suffix for the ids in its card, so a list of cards
// never repeats one.
func ruleDomID(email string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, strings.ToLower(email))
}

// vayuCardFilters is a mailbox's rules: the list in its order, and the editor.
func (a *App) vayuCardFilters(ctx context.Context, ac vmail.Account) string {
	lang := i18n.Lang(ctx)
	accts := a.vayuMail.Accounts()
	rules, _ := accts.FiltersFor(ctx, ac.Email)
	post := ` hx-post="/os/vayumail/filters/action" hx-target="#vm-accounts-list" hx-swap="innerHTML"`

	state := `<span class="muted text-xs">` + esc(i18n.T(lang, "none")) + `</span>`
	if len(rules) > 0 {
		state = `<span class="vm-count">` + strconv.Itoa(len(rules)) + `</span>`
	}
	var b strings.Builder
	b.WriteString(`<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">` + esc(i18n.T(lang, "Rules")) + `</span> ` + state + `</summary>`)
	b.WriteString(`<p class="muted text-xs">` + esc(i18n.T(lang, "Each message that arrives is checked against these, top to bottom. The first that matches decides, unless it says to keep going. A forward starts only after the address it sends to confirms by a link; rejected mail is refused while it is being delivered, so nothing is bounced back later.")) + `</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>#</th><th>` + esc(i18n.T(lang, "When, then")) + `</th><th></th></tr></thead><tbody>`)
	if len(rules) == 0 {
		b.WriteString(`<tr><td colspan="3" class="muted">` + esc(i18n.T(lang, "No rules yet.")) + `</td></tr>`)
	}
	for i, rl := range rules {
		when, then := ruleWords(lang, rl)
		id := strconv.FormatInt(rl.ID, 10)
		note := ""
		for _, act := range rl.Actions {
			if act.Kind == "forward" && !accts.RuleForwardConfirmed(ctx, ac.Email, act.Arg) {
				note = ` <span class="muted text-xs">` + esc(i18n.T(lang, "waiting for {address} to confirm", "address", act.Arg)) + `</span>`
			}
		}
		off := ""
		toggle := i18n.T(lang, "Switch off")
		if !rl.Enabled {
			off = ` <span class="muted text-xs">` + esc(i18n.T(lang, "switched off")) + `</span>`
			toggle = i18n.T(lang, "Switch on")
		}
		b.WriteString(`<tr><td class="muted">` + strconv.Itoa(i+1) + `</td><td>` + when + `<br><span class="muted text-xs">` + esc(i18n.T(lang, "then")) + `</span> ` + then + note + off + `</td><td><div class="vm-row">` +
			`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/filters/action" hx-target="#vm-rule-editor-` + ruleDomID(ac.Email) + `" hx-swap="outerHTML"` + hxVals("op", "edit", "email", ac.Email, "id", id) + `>` + esc(i18n.T(lang, "Edit")) + `</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "up", "email", ac.Email, "id", id) + ` aria-label="` + esc(i18n.T(lang, "Move up")) + `">` + esc(i18n.T(lang, "Up")) + `</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "down", "email", ac.Email, "id", id) + ` aria-label="` + esc(i18n.T(lang, "Move down")) + `">` + esc(i18n.T(lang, "Down")) + `</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "toggle", "email", ac.Email, "id", id) + `>` + esc(toggle) + `</button>` +
			`<button type="button" class="btn btn--sm btn--danger"` + post + hxVals("op", "delete", "email", ac.Email, "id", id) + `>` + esc(i18n.T(lang, "Delete")) + `</button>` +
			`</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(ruleEditor(lang, ac.Email, vmail.FilterRule{Enabled: true}, ""))
	b.WriteString(`</details>`)
	return b.String()
}

// ruleEditor is the form for a new rule, or for r when it has an ID.
func ruleEditor(lang, email string, r vmail.FilterRule, msg string) string {
	dom := ruleDomID(email)
	he := html.EscapeString(email)
	var b strings.Builder
	title := i18n.T(lang, "New rule")
	if r.ID > 0 {
		title = i18n.T(lang, "Edit rule")
	}
	b.WriteString(`<form id="vm-rule-editor-` + dom + `" class="vm-ooo-form" hx-post="/os/vayumail/filters/action" hx-target="#vm-accounts-list" hx-swap="innerHTML">`)
	b.WriteString(`<span class="field-label">` + esc(title) + `</span>`)
	b.WriteString(`<input type="hidden" name="op" value="save"><input type="hidden" name="email" value="` + he + `">`)
	if r.ID > 0 {
		b.WriteString(`<input type="hidden" name="id" value="` + strconv.FormatInt(r.ID, 10) + `">`)
	}
	anySel, allSel := "", " selected"
	if r.Any {
		anySel, allSel = " selected", ""
	}
	b.WriteString(`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "When")) + `</span><select class="input input--sm" name="match">` +
		`<option value="all"` + allSel + `>` + esc(i18n.T(lang, "all of these are true")) + `</option><option value="any"` + anySel + `>` + esc(i18n.T(lang, "any of these is true")) + `</option></select></label>`)
	for i := 0; i < ruleEditorRows; i++ {
		var c vmail.RuleCondition
		if i < len(r.Conditions) {
			c = r.Conditions[i]
		}
		n := strconv.Itoa(i)
		b.WriteString(`<div class="vm-row">`)
		b.WriteString(`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Condition {n}", "n", i+1)) + `</span><select class="input input--sm" name="c_field_` + n + `"><option value="">—</option>`)
		for _, f := range ruleFieldNames(lang) {
			b.WriteString(option(f[0], f[1], c.Field))
		}
		b.WriteString(`</select></label><label class="field"><span class="field-label">` + esc(i18n.T(lang, "Test")) + `</span><select class="input input--sm" name="c_op_` + n + `">`)
		for _, o := range ruleOpNames(lang) {
			b.WriteString(option(o[0], o[1], c.Op))
		}
		b.WriteString(`</select></label>`)
		b.WriteString(`<label class="field vm-grow"><span class="field-label">` + esc(i18n.T(lang, "Words")) + `</span><input class="input input--sm" type="text" name="c_value_` + n + `" value="` + html.EscapeString(c.Value) + `"></label>`)
		b.WriteString(`<label class="field"><span class="field-label">` + esc(i18n.T(lang, "Header name")) + `</span><input class="input input--sm" type="text" name="c_name_` + n + `" value="` + html.EscapeString(c.Name) + `" placeholder="` + esc(i18n.T(lang, "for Header only")) + `"></label>`)
		b.WriteString(`</div>`)
	}
	for i := 0; i < ruleEditorRows; i++ {
		var act vmail.RuleAction
		if i < len(r.Actions) {
			act = r.Actions[i]
		}
		n := strconv.Itoa(i)
		b.WriteString(`<div class="vm-row"><label class="field"><span class="field-label">` + esc(i18n.T(lang, "Then {n}", "n", i+1)) + `</span><select class="input input--sm" name="a_kind_` + n + `"><option value="">—</option>`)
		for _, k := range ruleActionNames(lang) {
			b.WriteString(option(k[0], k[1], act.Kind))
		}
		b.WriteString(`</select></label><label class="field vm-grow"><span class="field-label">` + esc(i18n.T(lang, "Folder, label or address")) + `</span><input class="input input--sm" type="text" name="a_arg_` + n + `" value="` + html.EscapeString(act.Arg) + `"></label></div>`)
	}
	keep := ""
	if r.KeepGoing {
		keep = " checked"
	}
	b.WriteString(`<label class="vm-row text-sm"><input type="checkbox" name="keep" value="1"` + keep + `> ` + esc(i18n.T(lang, "Keep going to the rules below")) + `</label>`)
	b.WriteString(`<div class="vm-row"><button class="btn btn--primary btn--sm" type="submit">` + esc(i18n.T(lang, "Save")) + `</button>` +
		`<button class="btn btn--sm" type="submit" name="run" value="1">` + esc(i18n.T(lang, "Save and run on existing mail")) + `</button>` +
		`<button class="btn btn--sm" type="button" hx-post="/os/vayumail/filters/action" hx-include="closest form" hx-vals='{"op":"test"}' hx-target="#vm-rule-test-` + dom + `" hx-swap="innerHTML">` + esc(i18n.T(lang, "Test on recent mail")) + `</button></div>`)
	b.WriteString(`<div id="vm-rule-test-` + dom + `" aria-live="polite">` + msg + `</div></form>`)
	return b.String()
}

func option(value, label, selected string) string {
	sel := ""
	if value == selected {
		sel = " selected"
	}
	return `<option value="` + html.EscapeString(value) + `"` + sel + `>` + html.EscapeString(label) + `</option>`
}

// ruleFromForm reads the editor's form into a rule for email.
func ruleFromForm(r *http.Request, email string) vmail.FilterRule {
	rule := vmail.FilterRule{Mailbox: email, Any: r.FormValue("match") == "any", KeepGoing: r.FormValue("keep") == "1", Enabled: true}
	rule.ID, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	for i := 0; i < ruleEditorRows; i++ {
		n := strconv.Itoa(i)
		if f := r.FormValue("c_field_" + n); f != "" {
			rule.Conditions = append(rule.Conditions, vmail.RuleCondition{Field: f, Op: r.FormValue("c_op_" + n), Value: r.FormValue("c_value_" + n), Name: r.FormValue("c_name_" + n)})
		}
		if k := r.FormValue("a_kind_" + n); k != "" {
			rule.Actions = append(rule.Actions, vmail.RuleAction{Kind: k, Arg: r.FormValue("a_arg_" + n)})
		}
	}
	return rule
}

// handleVayuOSFilterAction saves, orders, switches, deletes, tests and runs a
// mailbox's rules. An administrator, or the mailbox's holder.
func (a *App) handleVayuOSFilterAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	_ = r.ParseForm()
	lang := i18n.Lang(r.Context())
	accts := a.vayuMail.Accounts()
	email := strings.TrimSpace(r.FormValue("email"))
	if !a.mayChangeMailboxSettings(r, email) {
		code, msg := a.settingsRefusal(lang, email)
		writeAPIError(w, r, http.StatusForbidden, code, msg, "")
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	rd := a.mailReader(r, mailKeyOf(email, a.vayuMail.Config().Domain))
	var opErr error
	note := ""
	switch r.FormValue("op") {
	case "create":
		// The one-condition form of the first rules, still posted by older pages.
		action, target := r.FormValue("action"), ""
		if f, ok := strings.CutPrefix(action, "move:"); ok {
			action, target = "move", f
		}
		opErr = accts.CreateFilter(r.Context(), vmail.FilterRule{Mailbox: email, Field: r.FormValue("field"), Contains: r.FormValue("contains"), Action: action, Target: target})
		if opErr == nil {
			dbpkg.AuditLog("vayumail.filter.create", dbpkg.AuditActor(r), email, r.FormValue("field")+"~"+r.FormValue("contains"))
		}
	case "save":
		rule := ruleFromForm(r, email)
		var saved int64
		if saved, opErr = accts.SaveRule(r.Context(), rule); opErr == nil {
			rule.ID = saved
			dbpkg.AuditLog("vayumail.filter.save", dbpkg.AuditActor(r), email, strconv.FormatInt(saved, 10))
			note = a.askRuleForwards(r, email, rule)
			if r.FormValue("run") == "1" {
				if n, err := a.vayuMail.RunRule(rd, rule, ruleLookBack); err != nil {
					note += saCallout("danger", esc(i18n.T(lang, "Saved, but not run on existing mail: {reason}.", "reason", err.Error())))
				} else {
					note += saCallout("ok", esc(i18n.T(lang, "{n, plural, one {Saved, and run on existing mail: # message changed.} other {Saved, and run on existing mail: # messages changed.}}", "n", n)))
				}
			}
		}
	case "edit":
		rules, _ := accts.FiltersFor(r.Context(), email)
		for _, rl := range rules {
			if rl.ID == id {
				// Drawn from the stored rule's mailbox, not the form's
				// address: equal once authorised, but only the stored one
				// is not the request's own text, which the code scanner
				// follows through ruleDomID into the page.
				writeOSHTML(w, r, ruleEditor(lang, rl.Mailbox, rl, ""))
				return
			}
		}
		opErr = errors.New("no such rule")
	case "test":
		matched, looked, err := a.vayuMail.RuleMatches(rd, ruleFromForm(r, email), ruleLookBack)
		if err != nil {
			writeOSHTML(w, r, saCallout("danger", html.EscapeString(err.Error())+"."))
			return
		}
		writeOSHTML(w, r, ruleTestResult(lang, matched, looked))
		return
	case "up", "down":
		opErr = accts.MoveRule(r.Context(), email, id, r.FormValue("op") == "up")
	case "toggle":
		on := true
		if rules, err := accts.FiltersFor(r.Context(), email); err == nil {
			for _, rl := range rules {
				if rl.ID == id {
					on = !rl.Enabled
				}
			}
		}
		opErr = accts.SetRuleEnabled(r.Context(), email, id, on)
	case "delete":
		if opErr = accts.DeleteFilter(r.Context(), email, id); opErr == nil {
			dbpkg.AuditLog("vayumail.filter.delete", dbpkg.AuditActor(r), email, r.FormValue("id"))
		}
	default:
		opErr = errors.New("unknown operation")
	}
	card := a.acctRefresh(r, email)
	if opErr != nil {
		card = saCallout("danger", html.EscapeString(opErr.Error())) + card
	}
	writeOSHTML(w, r, note+card)
}

// ruleTestResult says what a rule would have caught.
func ruleTestResult(lang string, matched []vmail.StoredMessage, looked int) string {
	var b strings.Builder
	b.WriteString(`<p class="text-sm">` + escFill(i18n.T(lang, "{n, plural, one {{matched} of your last # message.} other {{matched} of your last # messages.}}", "n", looked, "matched", htmlMark(0)),
		`<strong>`+esc(i18n.T(lang, "Would have matched {n}", "n", len(matched)))+`</strong>`) + `</p>`)
	if len(matched) > 0 {
		b.WriteString(`<ul class="text-sm">`)
		for i, m := range matched {
			if i == 10 {
				b.WriteString(`<li class="muted">` + esc(i18n.T(lang, "and {n} more", "n", len(matched)-10)) + `</li>`)
				break
			}
			b.WriteString(`<li>` + html.EscapeString(m.From) + `: ` + html.EscapeString(m.Subject) + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	return b.String()
}

// askRuleForwards sends each address a rule forwards to, that has not yet
// confirmed, a link to confirm it, and says so.
func (a *App) askRuleForwards(r *http.Request, email string, rule vmail.FilterRule) string {
	lang := i18n.Lang(r.Context())
	out := ""
	for _, act := range rule.Actions {
		if act.Kind != "forward" {
			continue
		}
		tok, err := a.vayuMail.Accounts().RequestRuleForward(r.Context(), email, act.Arg)
		if errors.Is(err, vmail.ErrRuleForwardWait) {
			out += saCallout("info", esc(i18n.T(lang, "No new link was sent to {address}: one was sent in the last day, or this mailbox has asked as many addresses as a day allows. Nothing is forwarded until it is confirmed.", "address", act.Arg)))
			continue
		}
		if err != nil {
			out += saCallout("danger", esc(i18n.T(lang, "The confirmation to {address} could not be made: {reason}.", "address", act.Arg, "reason", err.Error())))
			continue
		}
		if tok == "" {
			continue
		}
		subject, body := ruleForwardMail(email, a.oauthBaseURL(r)+"/vayumail/forward/confirm?t="+url.QueryEscape(tok))
		if _, err := a.vayuMail.SendSystemMail(r.Context(), email, []string{act.Arg}, subject, "", body); err != nil {
			out += saCallout("danger", esc(i18n.T(lang, "The confirmation to {address} could not be sent: {reason}.", "address", act.Arg, "reason", err.Error())))
			continue
		}
		out += saCallout("info", esc(i18n.T(lang, "A link to confirm was sent to {address}. Nothing is forwarded until it is confirmed.", "address", act.Arg)))
	}
	return out
}

// handleRuleForwardConfirm is the page a forward's confirmation link opens. A
// GET only asks: mail scanners open links on their own, and a copy of
// someone's mail must not start because one did.
// ruleForwardMail is the mail asking an address to confirm a rule's copies.
// It goes to someone outside this install, whose language it does not know,
// so it is written in English, as mail between strangers usually is.
func ruleForwardMail(mailbox, link string) (subject, body string) {
	return "Confirm copies of " + mailbox + "'s mail",
		mailbox + " has made a rule that sends you copies of some of the mail it receives.\n\n" +
			"If you want those copies, confirm here:\n" + link + "\n\n" +
			"If you do not, do nothing: nothing is sent to you until this link is confirmed."
}

func (a *App) handleRuleForwardConfirm(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		http.NotFound(w, r)
		return
	}
	tok := r.FormValue("t")
	// The person confirming has no account here: their browser's language.
	lang := i18n.Choose("", r.Header.Get("Accept-Language"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	page := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="` + esc(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
			`<title>` + esc(i18n.T(lang, "Confirm forwarding")) + `</title></head><body>` + msg + `</body></html>`))
	}
	if r.Method != http.MethodPost {
		page(`<h1>` + esc(i18n.T(lang, "Receive copies of mail?")) + `</h1><p>` + esc(i18n.T(lang, "Someone has made a mail rule that sends copies of some of their mail to this address. Confirm to start receiving them.")) + `</p>` +
			`<form method="post"><input type="hidden" name="t" value="` + html.EscapeString(tok) + `"><button type="submit">` + esc(i18n.T(lang, "Confirm")) + `</button></form>`)
		return
	}
	mbox, addr, err := a.vayuMail.Accounts().ConfirmRuleForward(r.Context(), tok)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		page(`<h1>` + esc(i18n.T(lang, "Not confirmed")) + `</h1><p>` + esc(i18n.T(lang, "This link is not valid, was already used, or has expired.")) + `</p>`)
		return
	}
	dbpkg.AuditLog("vayumail.filter.forward-confirmed", addr, mbox, addr)
	page(`<h1>` + esc(i18n.T(lang, "Confirmed")) + `</h1><p>` + esc(i18n.T(lang, "{address} now receives the copies {mailbox}'s rule sends.", "address", addr, "mailbox", mbox)) + `</p>`)
}
