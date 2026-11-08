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

var ruleFieldNames = [][2]string{
	{"from", "From"}, {"to", "To or Cc"}, {"subject", "Subject"}, {"body", "Body"},
	{"domain", "Sender's domain"}, {"attachment", "Has an attachment"}, {"size", "Larger than (MB)"},
	{"list", "From a mailing list"}, {"header", "Header"},
}

var ruleOpNames = [][2]string{{"contains", "contains"}, {"not-contains", "does not contain"}, {"is", "is"}, {"ends-with", "ends with"}}

var ruleActionNames = [][2]string{
	{"move", "Move to"}, {"label", "Label"}, {"markread", "Mark read"}, {"pin", "Pin"},
	{"forward", "Forward to"}, {"reject", "Reject"}, {"delete", "Delete"},
}

func ruleName(list [][2]string, key string) string {
	for _, p := range list {
		if p[0] == key {
			return p[1]
		}
	}
	return key
}

// ruleWords says a rule as the list shows it.
func ruleWords(r vmail.FilterRule) (when, then string) {
	var conds []string
	for _, c := range r.Conditions {
		switch c.Field {
		case "attachment":
			conds = append(conds, "<b>has an attachment</b>")
		case "size":
			conds = append(conds, "<b>larger than "+html.EscapeString(c.Value)+" MB</b>")
		case "list":
			if c.Op == "any" {
				conds = append(conds, "<b>from a mailing list</b>")
				break
			}
			conds = append(conds, "<b>mailing list</b> "+ruleName(ruleOpNames, c.Op)+" <b>"+html.EscapeString(c.Value)+"</b>")
		case "header":
			conds = append(conds, "<b>"+html.EscapeString(c.Name)+"</b> "+ruleName(ruleOpNames, c.Op)+" <b>"+html.EscapeString(c.Value)+"</b>")
		default:
			conds = append(conds, "<b>"+strings.ToLower(ruleName(ruleFieldNames, c.Field))+"</b> "+ruleName(ruleOpNames, c.Op)+" <b>"+html.EscapeString(c.Value)+"</b>")
		}
	}
	join := " and "
	if r.Any {
		join = " or "
	}
	when = "When " + strings.Join(conds, join)
	var acts []string
	for _, a := range r.Actions {
		switch a.Kind {
		case "move":
			acts = append(acts, "move to <b>"+html.EscapeString(a.Arg)+"</b>")
		case "label":
			acts = append(acts, "label <b>"+html.EscapeString(a.Arg)+"</b>")
		case "forward":
			acts = append(acts, "forward to <b>"+html.EscapeString(a.Arg)+"</b>")
		case "reject":
			acts = append(acts, "<b>reject</b>: the sender is told it was refused, and nothing is kept")
		default:
			acts = append(acts, strings.ToLower(ruleName(ruleActionNames, a.Kind)))
		}
	}
	then = strings.Join(acts, ", ")
	if r.KeepGoing {
		then += ", and keep going"
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
	accts := a.vayuMail.Accounts()
	rules, _ := accts.FiltersFor(ctx, ac.Email)
	post := ` hx-post="/os/vayumail/filters/action" hx-target="#vm-accounts-list" hx-swap="innerHTML"`

	state := `<span class="muted text-xs">none</span>`
	if len(rules) > 0 {
		state = `<span class="vm-count">` + strconv.Itoa(len(rules)) + `</span>`
	}
	var b strings.Builder
	b.WriteString(`<details class="vm-ooo vm-acct__sub"><summary><span class="field-label">Rules</span> ` + state + `</summary>`)
	b.WriteString(`<p class="muted text-xs">Each message that arrives is checked against these, top to bottom. The first that matches decides, unless it says to keep going. ` +
		`A forward starts only after the address it sends to confirms by a link; rejected mail is refused while it is being delivered, so nothing is bounced back later.</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>#</th><th>When, then</th><th></th></tr></thead><tbody>`)
	if len(rules) == 0 {
		b.WriteString(`<tr><td colspan="3" class="muted">No rules yet.</td></tr>`)
	}
	for i, rl := range rules {
		when, then := ruleWords(rl)
		id := strconv.FormatInt(rl.ID, 10)
		note := ""
		for _, act := range rl.Actions {
			if act.Kind == "forward" && !accts.RuleForwardConfirmed(ctx, ac.Email, act.Arg) {
				note = ` <span class="muted text-xs">waiting for ` + html.EscapeString(act.Arg) + ` to confirm</span>`
			}
		}
		off := ""
		toggle := "Switch off"
		if !rl.Enabled {
			off = ` <span class="muted text-xs">switched off</span>`
			toggle = "Switch on"
		}
		b.WriteString(`<tr><td class="muted">` + strconv.Itoa(i+1) + `</td><td>` + when + `<br><span class="muted text-xs">then</span> ` + then + note + off + `</td><td><div class="vm-row">` +
			`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/filters/action" hx-target="#vm-rule-editor-` + ruleDomID(ac.Email) + `" hx-swap="outerHTML"` + hxVals("op", "edit", "email", ac.Email, "id", id) + `>Edit</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "up", "email", ac.Email, "id", id) + ` aria-label="Move up">Up</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "down", "email", ac.Email, "id", id) + ` aria-label="Move down">Down</button>` +
			`<button type="button" class="btn btn--sm"` + post + hxVals("op", "toggle", "email", ac.Email, "id", id) + `>` + toggle + `</button>` +
			`<button type="button" class="btn btn--sm btn--danger"` + post + hxVals("op", "delete", "email", ac.Email, "id", id) + `>Delete</button>` +
			`</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	b.WriteString(ruleEditor(ac.Email, vmail.FilterRule{Enabled: true}, ""))
	b.WriteString(`</details>`)
	return b.String()
}

// ruleEditor is the form for a new rule, or for r when it has an ID.
func ruleEditor(email string, r vmail.FilterRule, msg string) string {
	dom := ruleDomID(email)
	he := html.EscapeString(email)
	var b strings.Builder
	title := "New rule"
	if r.ID > 0 {
		title = "Edit rule"
	}
	b.WriteString(`<form id="vm-rule-editor-` + dom + `" class="vm-ooo-form" hx-post="/os/vayumail/filters/action" hx-target="#vm-accounts-list" hx-swap="innerHTML">`)
	b.WriteString(`<span class="field-label">` + title + `</span>`)
	b.WriteString(`<input type="hidden" name="op" value="save"><input type="hidden" name="email" value="` + he + `">`)
	if r.ID > 0 {
		b.WriteString(`<input type="hidden" name="id" value="` + strconv.FormatInt(r.ID, 10) + `">`)
	}
	anySel, allSel := "", " selected"
	if r.Any {
		anySel, allSel = " selected", ""
	}
	b.WriteString(`<label class="field"><span class="field-label">When</span><select class="input input--sm" name="match">` +
		`<option value="all"` + allSel + `>all of these are true</option><option value="any"` + anySel + `>any of these is true</option></select></label>`)
	for i := 0; i < ruleEditorRows; i++ {
		var c vmail.RuleCondition
		if i < len(r.Conditions) {
			c = r.Conditions[i]
		}
		n := strconv.Itoa(i)
		b.WriteString(`<div class="vm-row">`)
		b.WriteString(`<label class="field"><span class="field-label">Condition ` + strconv.Itoa(i+1) + `</span><select class="input input--sm" name="c_field_` + n + `"><option value="">—</option>`)
		for _, f := range ruleFieldNames {
			b.WriteString(option(f[0], f[1], c.Field))
		}
		b.WriteString(`</select></label><label class="field"><span class="field-label">Test</span><select class="input input--sm" name="c_op_` + n + `">`)
		for _, o := range ruleOpNames {
			b.WriteString(option(o[0], o[1], c.Op))
		}
		b.WriteString(`</select></label>`)
		b.WriteString(`<label class="field vm-grow"><span class="field-label">Words</span><input class="input input--sm" type="text" name="c_value_` + n + `" value="` + html.EscapeString(c.Value) + `"></label>`)
		b.WriteString(`<label class="field"><span class="field-label">Header name</span><input class="input input--sm" type="text" name="c_name_` + n + `" value="` + html.EscapeString(c.Name) + `" placeholder="for Header only"></label>`)
		b.WriteString(`</div>`)
	}
	for i := 0; i < ruleEditorRows; i++ {
		var act vmail.RuleAction
		if i < len(r.Actions) {
			act = r.Actions[i]
		}
		n := strconv.Itoa(i)
		b.WriteString(`<div class="vm-row"><label class="field"><span class="field-label">Then ` + strconv.Itoa(i+1) + `</span><select class="input input--sm" name="a_kind_` + n + `"><option value="">—</option>`)
		for _, k := range ruleActionNames {
			b.WriteString(option(k[0], k[1], act.Kind))
		}
		b.WriteString(`</select></label><label class="field vm-grow"><span class="field-label">Folder, label or address</span><input class="input input--sm" type="text" name="a_arg_` + n + `" value="` + html.EscapeString(act.Arg) + `"></label></div>`)
	}
	keep := ""
	if r.KeepGoing {
		keep = " checked"
	}
	b.WriteString(`<label class="vm-row text-sm"><input type="checkbox" name="keep" value="1"` + keep + `> Keep going to the rules below</label>`)
	b.WriteString(`<div class="vm-row"><button class="btn btn--primary btn--sm" type="submit">Save</button>` +
		`<button class="btn btn--sm" type="submit" name="run" value="1">Save and run on existing mail</button>` +
		`<button class="btn btn--sm" type="button" hx-post="/os/vayumail/filters/action" hx-include="closest form" hx-vals='{"op":"test"}' hx-target="#vm-rule-test-` + dom + `" hx-swap="innerHTML">Test on recent mail</button></div>`)
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
	accts := a.vayuMail.Accounts()
	email := strings.TrimSpace(r.FormValue("email"))
	if !a.mayChangeMailboxSettings(r, email) {
		code, msg := a.settingsRefusal(email)
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
					note += saCallout("danger", "Saved, but not run on existing mail: "+html.EscapeString(err.Error())+".")
				} else {
					note += saCallout("ok", "Saved, and run on existing mail: "+strconv.Itoa(n)+" message"+plural(n)+" changed.")
				}
			}
		}
	case "edit":
		rules, _ := accts.FiltersFor(r.Context(), email)
		for _, rl := range rules {
			if rl.ID == id {
				writeOSHTML(w, r, ruleEditor(email, rl, ""))
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
		writeOSHTML(w, r, ruleTestResult(matched, looked))
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
func ruleTestResult(matched []vmail.StoredMessage, looked int) string {
	var b strings.Builder
	b.WriteString(`<p class="text-sm"><strong>Would have matched ` + strconv.Itoa(len(matched)) + `</strong> of your last ` + strconv.Itoa(looked) + ` message` + plural(looked) + `.</p>`)
	if len(matched) > 0 {
		b.WriteString(`<ul class="text-sm">`)
		for i, m := range matched {
			if i == 10 {
				b.WriteString(`<li class="muted">and ` + strconv.Itoa(len(matched)-10) + ` more</li>`)
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
	out := ""
	for _, act := range rule.Actions {
		if act.Kind != "forward" {
			continue
		}
		tok, err := a.vayuMail.Accounts().RequestRuleForward(r.Context(), email, act.Arg)
		if errors.Is(err, vmail.ErrRuleForwardWait) {
			out += saCallout("info", "No new link was sent to "+html.EscapeString(act.Arg)+": one was sent in the last day, or this mailbox has asked as many addresses as a day allows. Nothing is forwarded until it is confirmed.")
			continue
		}
		if err != nil {
			out += saCallout("danger", "The confirmation to "+html.EscapeString(act.Arg)+" could not be made: "+html.EscapeString(err.Error())+".")
			continue
		}
		if tok == "" {
			continue
		}
		link := a.oauthBaseURL(r) + "/vayumail/forward/confirm?t=" + url.QueryEscape(tok)
		body := email + " has made a rule that sends you copies of some of the mail it receives.\n\n" +
			"If you want those copies, confirm here:\n" + link + "\n\n" +
			"If you do not, do nothing: nothing is sent to you until this link is confirmed."
		if _, err := a.vayuMail.SendSystemMail(r.Context(), email, []string{act.Arg}, "Confirm copies of "+email+"'s mail", "", body); err != nil {
			out += saCallout("danger", "The confirmation to "+html.EscapeString(act.Arg)+" could not be sent: "+html.EscapeString(err.Error())+".")
			continue
		}
		out += saCallout("info", "A link to confirm was sent to "+html.EscapeString(act.Arg)+". Nothing is forwarded until it is confirmed.")
	}
	return out
}

// handleRuleForwardConfirm is the page a forward's confirmation link opens. A
// GET only asks: mail scanners open links on their own, and a copy of
// someone's mail must not start because one did.
func (a *App) handleRuleForwardConfirm(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		http.NotFound(w, r)
		return
	}
	tok := r.FormValue("t")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	page := func(msg string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
			`<title>Confirm forwarding</title></head><body>` + msg + `</body></html>`))
	}
	if r.Method != http.MethodPost {
		page(`<h1>Receive copies of mail?</h1><p>Someone has made a mail rule that sends copies of some of their mail to this address. Confirm to start receiving them.</p>` +
			`<form method="post"><input type="hidden" name="t" value="` + html.EscapeString(tok) + `"><button type="submit">Confirm</button></form>`)
		return
	}
	mbox, addr, err := a.vayuMail.Accounts().ConfirmRuleForward(r.Context(), tok)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		page(`<h1>Not confirmed</h1><p>This link is not valid, was already used, or has expired.</p>`)
		return
	}
	dbpkg.AuditLog("vayumail.filter.forward-confirmed", addr, mbox, addr)
	page(`<h1>Confirmed</h1><p>` + html.EscapeString(addr) + ` now receives the copies ` + html.EscapeString(mbox) + `'s rule sends.</p>`)
}
