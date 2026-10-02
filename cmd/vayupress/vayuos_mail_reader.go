// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// The reader (Mail plan §3.3, render 01): one row of tools, the subject, the
// rest of the conversation folded above the message, the message's own head
// with its seal, the words, the attachments, and a reply field docked at the
// foot. The same card is the pane beside the list (pure HTMX) and the message
// page (the no-JavaScript and deep-link path, driven by admin-os-mail.js).

// readerView is how a message is shown: in the pane or as its own page, its
// HTML rendering or its text, remote images loaded or not, and whether it is
// only being looked at (the newest message, opened for the reader without
// being chosen: it is marked read after two seconds on screen, by the page).
type readerView struct {
	Pane, HTML, Images, Peek bool
}

// vayuReaderCard renders one message. It reads the message and, unless the
// view is a peek, marks it read (received folders only). ok is false when the
// message cannot be read.
func (a *App) vayuReaderCard(rd vmail.Reader, folder, id string, v readerView) (string, bool) {
	user := rd.Key()
	// A read-only mailbox is offered no control the engine would refuse: no
	// reply or forward (a send), no Junk, Trash, Restore or Move, no Delete.
	readOnly := a.vayuMail.ReaderReadOnly(rd)
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, id)
	if err != nil {
		return "", false
	}
	received := !strings.EqualFold(folder, "Drafts") && !strings.EqualFold(folder, "Sent")
	if received && !v.Peek {
		if nid, merr := a.vayuMail.MarkRead(rd, folder, id); merr == nil && nid != "" {
			id = nid
		}
	}
	pane := v.Pane
	mbox := mailAddrOf(user, a.cfgDomain())
	msgURL := func(mid string) string {
		return "/os/vayumail/message?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(mid)
	}

	// Where this message stands in its folder, and its conversation.
	msgs, _ := a.vayuMail.ListFolder(rd, folder)
	var cur vmail.StoredMessage
	pos, newerID, olderID := -1, "", ""
	for i, m := range msgs {
		if m.ID == id {
			cur, pos = m, i
			if i > 0 {
				newerID = msgs[i-1].ID
			}
			if i+1 < len(msgs) {
				olderID = msgs[i+1].ID
			}
			break
		}
	}
	convo := a.mailConversation(rd, folder, cur, msgs)

	pm := vmail.ParseMessage(raw)
	subj := strings.TrimSpace(pm.Subject)
	if subj == "" {
		subj = "(no subject)"
	}
	fromName, fromAddr := mailParseFrom(pm.From)
	who := fromName
	if who == "" {
		who = fromAddr
	}
	q := "user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(id)

	var c strings.Builder
	c.WriteString(`<div class="card vm-reader mx-reader" data-mx-reader data-mx-href="` + esc(msgURL(id)) + `"`)
	if v.Peek && received && !cur.Seen {
		c.WriteString(` data-mx-peek="` + esc(id) + `" data-mx-user="` + esc(user) + `" data-mx-folder="` + esc(folder) + `"`)
	}
	c.WriteString(`>`)

	// ── The tools: what changes the message on the left, answers and the way
	// through the folder on the right. The pane's are HTMX posts that redraw
	// the pane and refresh the list; the page's are admin-os-mail.js's.
	paneVals := func(extra ...string) string {
		args := append([]string{"user", user, "folder", folder, "id", id}, extra...)
		if v.HTML {
			args = append(args, "html", "1")
		}
		if v.Images {
			args = append(args, "images", "1")
		}
		return hxVals(args...)
	}
	hxPost := ` hx-post="/os/vayumail/message/pane-action" hx-target="#vm-readpane" hx-swap="innerHTML" `
	tool := func(icon, label, key, paneArgs, pageAttr string, danger bool) {
		title := label
		if key != "" {
			title += " (" + key + ")"
		}
		cls := "mx-tool"
		if danger {
			cls += " mx-tool--danger"
		}
		attrs := pageAttr
		if pane {
			attrs = hxPost + paneArgs
		}
		c.WriteString(`<button type="button" class="` + cls + `" title="` + esc(title) + `" aria-label="` + esc(label) + `"` + mailCmd(label, "This message", key) + ` ` + attrs + `>` + saIcon(icon) + `</button>`)
	}
	c.WriteString(`<div class="mx-rtools" role="toolbar" aria-label="Message"`)
	if !pane {
		// admin-os-mail.js reads the message it acts on from here.
		c.WriteString(` data-mail-actions data-user="` + esc(user) + `" data-folder="` + esc(folder) + `" data-id="` + esc(id) + `"`)
		if olderID != "" {
			c.WriteString(` data-next-id="` + esc(olderID) + `"`)
		}
	}
	c.WriteString(`><div class="mx-rtools__group">`)
	if !readOnly {
		if !strings.EqualFold(folder, "Archive") {
			tool("archive", "Archive", "e", paneVals("to", "Archive"), `data-mail-move="Archive"`, false)
		}
		if strings.EqualFold(folder, "Trash") {
			tool("inbox", "Restore to Inbox", "", paneVals("to", "Inbox"), `data-mail-move="Inbox"`, false)
			tool("trash", "Delete for good", "#", paneVals("delete", "1")+` hx-confirm="Delete this message for good?"`, `data-mail-delete`, true)
		} else {
			tool("trash", "Move to Trash", "#", paneVals("to", "Trash"), `data-mail-move="Trash"`, false)
		}
		if !strings.EqualFold(folder, "Junk") {
			tool("spam", "Junk", "!", paneVals("to", "Junk"), `data-mail-move="Junk"`, false)
		}
		// Move: a menu of the folders this message can go to.
		c.WriteString(`<details class="sa-pop"><summary class="mx-tool" title="Move to… (v)" aria-label="Move to">` + saIcon("move") + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
		for _, f := range vmail.StandardFolders {
			// Snoozed is excluded: only the snooze action files there.
			if strings.EqualFold(f, folder) || strings.EqualFold(f, "Snoozed") {
				continue
			}
			attrs := `data-mail-move="` + esc(f) + `"`
			if pane {
				attrs = hxPost + paneVals("to", f)
			}
			c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" ` + attrs + `>` + saIcon(mailFolderIcons[f]) + `<span class="sa-menu__text">` + esc(f) + `</span></button>`)
		}
		c.WriteString(`</div></details>`)
	}
	c.WriteString(`<span class="mx-rtools__sep" aria-hidden="true"></span>`)
	if pane && received && !readOnly && !strings.EqualFold(folder, "Snoozed") {
		// "Later" (+4h) existed in the engine but had no button, so the
		// fastest snooze the product could offer was "tomorrow".
		c.WriteString(`<details class="sa-pop"><summary class="mx-tool" title="Snooze (s)" aria-label="Snooze">` + saIcon("timer") + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
		for _, s := range [][2]string{{"later", "Later today"}, {"tomorrow", "Tomorrow, 8:00"}, {"nextweek", "Next week, Monday 8:00"}} {
			c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem"` + hxPost + paneVals("snooze", s[0]) + `><span class="sa-menu__text">` + s[1] + `</span></button>`)
		}
		c.WriteString(`</div></details>`)
	}
	pinArgs, pinPage, pinLabel := paneVals("pin", "1"), `data-mail-pin="1"`, "Pin"
	if cur.Flagged {
		pinArgs, pinPage, pinLabel = paneVals("pin", "0"), `data-mail-pin="0"`, "Unpin"
	}
	tool("pin", pinLabel, "p", pinArgs, pinPage, false)
	c.WriteString(`</div><span class="mx-rtools__fill"></span><div class="mx-rtools__group">`)
	link := func(icon, label, key, href string) {
		c.WriteString(`<a class="mx-tool" href="` + esc(href) + `" title="` + esc(label+" ("+key+")") + `" aria-label="` + esc(label) + `"` + mailCmd(label, "This message", key) + `>` + saIcon(icon) + `</a>`)
	}
	if !readOnly {
		link("reply", "Reply", "r", "/os/vayumail/compose?reply=1&"+q)
		link("reply-all", "Reply all", "a", "/os/vayumail/compose?replyall=1&"+q)
		link("forward", "Forward", "f", "/os/vayumail/compose?forward=1&"+q)
		c.WriteString(`<span class="mx-rtools__sep" aria-hidden="true"></span>`)
	}
	if pos >= 0 {
		c.WriteString(`<span class="mx-rtools__pos">` + strconv.Itoa(pos+1) + ` of ` + strconv.Itoa(len(msgs)) + `</span>`)
	}
	nav := func(mid, icon, label, key, travel string) {
		if mid == "" {
			c.WriteString(`<span class="mx-tool" aria-disabled="true" aria-label="` + label + `">` + saIcon(icon) + `</span>`)
			return
		}
		if pane {
			c.WriteString(`<button type="button" class="mx-tool" data-mx-travel="` + travel + `" hx-get="` + esc(msgURL(mid)) + `&amp;pane=1" hx-target="#vm-readpane" hx-swap="innerHTML" title="` + label + ` (` + key + `)" aria-label="` + label + `">` + saIcon(icon) + `</button>`)
			return
		}
		c.WriteString(`<a class="mx-tool" href="` + esc(msgURL(mid)) + `" title="` + label + ` (` + key + `)" aria-label="` + label + `">` + saIcon(icon) + `</a>`)
	}
	nav(newerID, "chev-u", "Newer message", "k", "newer")
	nav(olderID, "chev-d", "Older message", "j", "older")

	// More: the rarer things, folded.
	view := func(label string, htmlOn, imagesOn bool) string {
		u := msgURL(id)
		if pane {
			u += "&pane=1"
		}
		if htmlOn {
			u += "&html=1"
		}
		if imagesOn {
			u += "&images=1"
		}
		hx := ""
		if pane {
			hx = ` hx-get="` + esc(u) + `" hx-target="#vm-readpane" hx-swap="innerHTML"`
		}
		return `<a class="sa-menu__item" role="menuitem" href="` + esc(u) + `"` + hx + `><span class="sa-menu__text">` + label + `</span></a>`
	}
	hasHTML := strings.TrimSpace(pm.HTML) != ""
	c.WriteString(`<details class="sa-pop"><summary class="mx-tool" title="More" aria-label="More">` + saIcon("more") + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
	if hasHTML && !v.HTML && strings.TrimSpace(pm.Text) != "" {
		c.WriteString(view("Show as the sender styled it", true, false))
	}
	if hasHTML && v.HTML {
		c.WriteString(view("Show as plain text", false, false))
	}
	if hasHTML && !v.Images {
		c.WriteString(view("Load images", true, true))
	}
	if received {
		attrs := `data-mail-mark="unread"`
		if pane {
			attrs = hxPost + paneVals("mark", "unread")
		}
		c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" ` + attrs + `><span class="sa-menu__text">Mark unread</span><kbd class="sa-menu__key">u</kbd></button>`)
	}
	c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" data-vm-print><span class="sa-menu__text">Print</span></button>`)
	if !readOnly && !strings.EqualFold(folder, "Trash") {
		attrs := `data-mail-delete`
		if pane {
			attrs = hxPost + paneVals("delete", "1") + ` hx-confirm="Delete this message for good?"`
		}
		c.WriteString(`<button type="button" class="sa-menu__item sa-menu__item--danger" role="menuitem" ` + attrs + `><span class="sa-menu__text">Delete for good</span></button>`)
	}
	if pane {
		c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" hx-get="/os/vayumail/inbox/readpane" hx-target="#vm-readpane" hx-swap="innerHTML"><span class="sa-menu__text">Close</span></button>`)
	} else {
		c.WriteString(`<a class="sa-menu__item" role="menuitem" href="/os/vayumail/inbox?user=` + qparam(user) + `&amp;folder=` + qparam(folder) + `"><span class="sa-menu__text">Back to ` + esc(folder) + `</span></a>`)
	}
	c.WriteString(`</div></details></div></div>`)

	// ── The message itself, scrolling under the tools.
	c.WriteString(`<div class="mx-rbody">`)
	n := len(convo) + 1
	c.WriteString(`<header><h2 class="mx-subject">` + esc(subj) + `</h2><p class="mx-subline">` + esc(folder))
	if n > 1 {
		c.WriteString(` <span aria-hidden="true">·</span> ` + strconv.Itoa(n) + ` messages`)
	}
	c.WriteString(`</p></header>`)

	// The rest of the conversation, folded to one line each above the
	// message; a line opens in place, and loads its words only then.
	if len(convo) > 0 {
		c.WriteString(`<div class="mx-convo">`)
		avSet := a.mailboxAvatarSet()
		for _, m := range convo {
			mf := m.msg.From
			name, _ := mailParseFrom(mf)
			if name == "" {
				name = mailDisplay(mf)
			}
			body := "/os/vayumail/message/body?user=" + qparam(user) + "&folder=" + qparam(m.folder) + "&id=" + qparam(m.msg.ID)
			c.WriteString(`<details class="mx-earlier" hx-get="` + esc(body) + `" hx-trigger="toggle once" hx-target="find .mx-earlier__body" hx-swap="innerHTML">` +
				`<summary class="mx-earlier__row">` + mailAvatarImg(mf, avSet) + `<span class="mx-earlier__who">` + esc(name) + `</span>` +
				`<span class="mx-earlier__pv">` + esc(m.msg.Preview) + `</span><time class="mx-earlier__time">` + esc(mailListTime(m.msg.Date, time.Now())) + `</time></summary>` +
				`<div class="mx-earlier__body"></div></details>`)
		}
		c.WriteString(`</div>`)
	}

	c.WriteString(`<article>`)
	c.WriteString(`<div class="mx-msg__head">` + mailAvatarImg(pm.From, a.mailboxAvatarSet()) + `<div class="mx-msg__who"><div class="mx-msg__from"><span class="mx-msg__name">` + esc(who) + `</span>`)
	if fromAddr != "" && fromAddr != who {
		c.WriteString(` <span class="mx-msg__addr">` + esc(fromAddr) + `</span>`)
	}
	c.WriteString(` ` + contactSaveButton(user, pm.From) + `</div>`)
	if to := mailRecipientsLine(pm.To, pm.Cc, mbox); to != "" {
		c.WriteString(`<div class="mx-msg__to">` + esc(to) + `</div>`)
	}
	c.WriteString(`</div>`)
	if !cur.Date.IsZero() {
		c.WriteString(`<time class="mx-msg__time" datetime="` + cur.Date.UTC().Format(time.RFC3339) + `" title="` + esc(config.FormatSiteStamp(cur.Date)) + `">` + esc(mailReaderTime(cur.Date, time.Now())) + `</time>`)
	}
	c.WriteString(`</div>`)
	// The seal comes from what was verified, read from the message as it was
	// delivered (decrypting it for display drops the signature).
	if stored, err := a.vayuMail.ReadFolderMessageStored(rd, folder, id); err == nil {
		var checker sealChecker
		if a.vayuPGP != nil {
			checker = a.vayuPGP
		}
		if s := mailSealFor(checker, mbox, stored, fromAddr, who); s.Text != "" {
			icon := "lock"
			if !strings.Contains(s.Text, "ncrypted") {
				icon = "check-c"
			}
			c.WriteString(`<p class="mx-seal mx-seal--` + map[string]string{"ok": "ok", "danger": "danger", "": "plain"}[s.Tone] + `">` + saIcon(icon) + `<span>` + esc(s.Text) + `</span></p>`)
		}
	}
	c.WriteString(`<div class="mx-msg__body">` + a.mailBodyHTML(pm, raw, v) + `</div>`)

	// Attachments as cards: a type tile, the name, the size, and download.
	if len(pm.Attachments) > 0 {
		c.WriteString(`<ul class="mx-files" aria-label="Attachments">`)
		for _, att := range pm.Attachments {
			dl := "/os/vayumail/attachment?" + q + "&idx=" + itoaSafe(att.Index)
			c.WriteString(`<li><a class="mx-file" href="` + esc(dl) + `" download><span class="mx-file__type" aria-hidden="true">` + esc(mailFileType(att.Filename, att.ContentType)) + `</span>` +
				`<span class="mx-file__meta"><span class="mx-file__name">` + esc(att.Filename) + `</span><span class="mx-file__size">` + esc(humanBytes(att.Size)) + `</span></span>` +
				`<span class="mx-file__dl" aria-hidden="true">` + saIcon("download") + `</span><span class="vp-sr-only">Download</span></a></li>`)
		}
		c.WriteString(`</ul>`)
	}
	// The message as it arrived, for the rare day it is needed.
	c.WriteString(`<details class="mx-source"><summary>Show the original</summary><pre class="vm-pre vm-raw">` + esc(string(raw)) + `</pre></details>`)
	c.WriteString(`</article></div>`)

	// Reply, docked: a field that opens the reply with nothing lost.
	if !readOnly && received && fromAddr != "" {
		first := strings.Fields(who)
		name := who
		if len(first) > 0 {
			name = first[0]
		}
		c.WriteString(`<div class="mx-dock"><a class="mx-dock__field" href="/os/vayumail/compose?reply=1&amp;` + esc(q) + `">Reply to ` + esc(name) + `…</a>`)
		if a.vayuPGP != nil {
			if _, err := a.vayuPGP.GetPublicKey(fromAddr); err == nil {
				c.WriteString(`<span class="mx-dock__enc">` + saIcon("lock") + ` Encrypted</span>`)
			}
		}
		c.WriteString(`</div>`)
	}
	c.WriteString(`</div>`)
	return c.String(), true
}

// mailBodyHTML is a message's words as the reader shows them. Plain text stays
// the default (it is what the sender's words actually are); a message that is
// only HTML, or one the reader asked to see styled, goes through the mail
// sanitiser with remote images off unless asked for.
func (a *App) mailBodyHTML(pm vmail.ParsedMessage, raw []byte, v readerView) string {
	hasHTML := strings.TrimSpace(pm.HTML) != ""
	note := `<p class="mx-images-off">Pictures stay off until you ask: loading one tells the sender you opened this message.</p>`
	switch {
	case hasHTML && v.HTML:
		if v.Images {
			return `<div class="vm-html">` + mailHTMLPolicyImages.Sanitize(pm.HTML) + `</div>`
		}
		return note + `<div class="vm-html">` + mailHTMLNoImages(pm.HTML) + `</div>`
	case strings.TrimSpace(pm.Text) != "":
		main, quoted := splitQuoted(pm.Text)
		out := `<div class="mx-text">` + esc(strings.TrimSpace(main)) + `</div>`
		if quoted != "" {
			out += `<details class="mx-quote"><summary>Show the quoted message</summary><div class="mx-text mx-text--quoted">` + esc(quoted) + `</div></details>`
		}
		return out
	case hasHTML:
		return `<div class="vm-html">` + mailHTMLNoImages(pm.HTML) + `</div>`
	}
	return `<div class="mx-text">` + esc(string(raw)) + `</div>`
}

// convoItem is one other message of a conversation, with the folder it is in.
type convoItem struct {
	msg    vmail.StoredMessage
	folder string
}

// mailConversation is the rest of cur's conversation, oldest first: the
// messages of this folder and of Sent that share its thread (mailThreadKeyer),
// so a reply of yours sits between the two of theirs where it belongs.
func (a *App) mailConversation(rd vmail.Reader, folder string, cur vmail.StoredMessage, msgs []vmail.StoredMessage) []convoItem {
	if cur.ID == "" {
		return nil
	}
	pool := make([]convoItem, 0, len(msgs))
	for _, m := range msgs {
		pool = append(pool, convoItem{m, folder})
	}
	if !strings.EqualFold(folder, "Sent") {
		if sent, err := a.vayuMail.ListFolder(rd, "Sent"); err == nil {
			for _, m := range sent {
				pool = append(pool, convoItem{m, "Sent"})
			}
		}
	}
	all := make([]vmail.StoredMessage, len(pool))
	for i, p := range pool {
		all[i] = p.msg
	}
	key := mailThreadKeyer(all)
	k := key(cur)
	if k == "" || strings.HasPrefix(k, "s:") {
		return nil // only ids join a conversation across folders
	}
	var out []convoItem
	for _, p := range pool {
		if (p.msg.ID == cur.ID && p.folder == folder) || key(p.msg) != k {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].msg.Date.Before(out[j].msg.Date) })
	return out
}

// mailRecipientsLine says who a message went to in the reader's terms:
// "to me, cc Mehul Shah", the mailbox reading it called "me".
func mailRecipientsLine(to, cc, me string) string {
	name := func(s string) string {
		n, addr := mailParseFrom(s)
		if strings.EqualFold(addr, me) {
			return "me"
		}
		if n != "" {
			return n
		}
		return addr
	}
	list := func(h string) []string {
		var out []string
		for _, p := range strings.Split(h, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, name(p))
			}
		}
		return out
	}
	line := ""
	if t := list(to); len(t) > 0 {
		line = "to " + strings.Join(t, ", ")
	}
	if c := list(cc); len(c) > 0 {
		if line != "" {
			line += ", "
		}
		line += "cc " + strings.Join(c, ", ")
	}
	return line
}

// mailReaderTime is a message's time in full enough words to stand alone:
// "Today, 14:20", "Yesterday, 09:05", "Thu 2 Oct, 14:20".
func mailReaderTime(t, now time.Time) string {
	switch mailListGroup(t, now) {
	case "Today":
		return "Today, " + config.FormatSite(t, "15:04")
	case "Yesterday":
		return "Yesterday, " + config.FormatSite(t, "15:04")
	}
	if config.InSite(t).Year() == config.InSite(now).Year() {
		return config.FormatSite(t, "Mon 2 Jan, 15:04")
	}
	return config.FormatSite(t, "2 Jan 2006, 15:04")
}

// mailFileType is the short type an attachment's tile shows: its extension,
// or the kind of its media type.
func mailFileType(name, ctype string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 && len(name)-i <= 5 && i < len(name)-1 {
		return strings.ToUpper(name[i+1:])
	}
	if i := strings.IndexByte(ctype, '/'); i > 0 {
		return strings.ToUpper(ctype[:min(i, 4)])
	}
	return "FILE"
}

// handleVayuOSMessageBody is one message's words alone, for a folded line of
// a conversation opened in place. It marks nothing read: opening a line is
// looking back, not reading new mail.
func (a *App) handleVayuOSMessageBody(w http.ResponseWriter, r *http.Request) {
	if !a.mailRunning() {
		writeOSFragment(w, "")
		return
	}
	rd := a.mailReader(r, mailUserParam(r))
	folder := mailFolderParam(r)
	if folder == "" {
		folder = "Inbox"
	}
	raw, err := a.vayuMail.ReadFolderMessage(rd, folder, mailIDParam(r))
	if rd.Key() == "" || err != nil {
		writeOSFragment(w, `<p class="mx-empty">This message could not be read.</p>`)
		return
	}
	writeOSFragment(w, a.mailBodyHTML(vmail.ParseMessage(raw), raw, readerView{Pane: true}))
}

// mailCmd marks a control for the command bar, which offers what the page
// offers by name (Mail plan §6): its label, where it acts, and its key.
func mailCmd(label, where, key string) string {
	out := ` data-cmd="` + esc(label) + `" data-cmd-where="` + esc(where) + `"`
	if key != "" {
		out += ` aria-keyshortcuts="` + esc(key) + `"`
	}
	return out
}
