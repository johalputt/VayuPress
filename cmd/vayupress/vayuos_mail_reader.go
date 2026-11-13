// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"github.com/johalputt/vayupress/internal/i18n"
	"net/http"
	"sort"
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
	// Home is the zone an invitation's time is read in: the holder's, from
	// the request; the install's when nil.
	Home *time.Location
	// Places is which places beside Mail the session is offered: an
	// invitation is answered into the Calendar and an attachment saved into
	// Drive only where there is one to reach.
	Places holderPlaces
}

// vayuReaderCard renders one message. It reads the message and, unless the
// view is a peek, marks it read (received folders only). ok is false when the
// message cannot be read.
func (a *App) vayuReaderCard(rd vmail.Reader, folder, id string, v readerView) (string, bool) {
	user, lang := rd.Key(), rd.Lang()
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
	var msgLabels []string
	if byMsg, err := a.vayuMail.LabelsByMessage(rd); err == nil {
		msgLabels = byMsg[cur.MessageID]
	}

	pm := vmail.ParseMessage(raw)
	subj := strings.TrimSpace(pm.Subject)
	if subj == "" {
		subj = i18n.T(lang, "(no subject)")
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
	// word is the tool's visible word ("" for an icon alone); the most used
	// tools say what they do, and fold to their icons when the reader is
	// narrow (the reader is a container, vayuos.css).
	words := func(word string) string {
		if word == "" {
			return ""
		}
		return `<span class="mx-tool__word">` + esc(word) + `</span>`
	}
	here := i18n.T(lang, "This message")
	tool := func(icon, label, key, paneArgs, pageAttr string, danger bool, word string) {
		title := label
		if key != "" {
			title += " (" + key + ")"
		}
		cls := "mx-tool"
		if danger {
			cls += " mx-tool--danger"
		}
		if word != "" {
			cls += " mx-tool--word"
		}
		attrs := pageAttr
		if pane {
			attrs = hxPost + paneArgs
		}
		c.WriteString(`<button type="button" class="` + cls + `" title="` + esc(title) + `" aria-label="` + esc(label) + `"` + mailCmd(label, here, key) + ` ` + attrs + `>` + saIcon(icon) + words(word) + `</button>`)
	}
	c.WriteString(`<div class="mx-rtools" role="toolbar" aria-label="` + esc(i18n.T(lang, "Message")) + `"`)
	if !pane {
		// admin-os-mail.js reads the message it acts on from here.
		c.WriteString(` data-mail-actions data-user="` + esc(user) + `" data-folder="` + esc(folder) + `" data-id="` + esc(id) + `"`)
		if olderID != "" {
			c.WriteString(` data-next-id="` + esc(olderID) + `"`)
		}
	}
	c.WriteString(`><div class="mx-rtools__group">`)
	if pane {
		// Narrower than three columns the message is pushed over the list,
		// and this goes back to it (admin-os-mail.js).
		c.WriteString(`<button type="button" class="mx-back" data-mx-go="list">` + saIcon("chev-l") + esc(mailFolderLabel(lang, folder)) + `</button>`)
	}
	link := func(icon, label, key, href, cls string) {
		c.WriteString(`<a class="mx-tool mx-tool--word ` + cls + `" href="` + esc(href) + `" title="` + esc(label+" ("+key+")") + `" aria-label="` + esc(label) + `"` + mailCmd(label, here, key) + `>` + saIcon(icon) + words(label) + `</a>`)
	}
	if !readOnly {
		// What is done to a message most: answer it. First, with its words;
		// Reply keeps its word the longest (vayuos.css).
		link("reply", i18n.T(lang, "Reply"), "r", "/os/vayumail/compose?reply=1&"+q, "mx-tool--answer mx-tool--reply")
		link("reply-all", i18n.T(lang, "Reply all"), "a", "/os/vayumail/compose?replyall=1&"+q, "mx-tool--answer")
		link("forward", i18n.T(lang, "Forward"), "f", "/os/vayumail/compose?forward=1&"+q, "mx-tool--answer")
		c.WriteString(`<span class="mx-rtools__sep" aria-hidden="true"></span>`)
	}
	if !readOnly {
		// In Junk the first thing offered is to say it is not (pipeline 3ab),
		// on the key that files a message there everywhere else.
		if strings.EqualFold(folder, "Junk") {
			tool("inbox", i18n.T(lang, "Not junk"), "!", paneVals("notjunk", "1"), `data-mail-notjunk`, false, i18n.T(lang, "Not junk"))
		}
		if !strings.EqualFold(folder, "Archive") {
			tool("archive", i18n.T(lang, "Archive"), "e", paneVals("to", "Archive"), `data-mail-move="Archive"`, false, i18n.T(lang, "Archive"))
		}
		if strings.EqualFold(folder, "Trash") {
			tool("inbox", i18n.T(lang, "Restore to Inbox"), "", paneVals("to", "Inbox"), `data-mail-move="Inbox"`, false, i18n.T(lang, "Restore"))
			tool("trash", i18n.T(lang, "Delete for good"), "#", paneVals("delete", "1")+` hx-confirm="`+esc(i18n.T(lang, "Delete this message for good?"))+`"`, `data-mail-delete`, true, i18n.T(lang, "Delete for good"))
		} else {
			tool("trash", i18n.T(lang, "Move to Trash"), "#", paneVals("to", "Trash"), `data-mail-move="Trash"`, false, i18n.T(lang, "Delete"))
		}
		if !strings.EqualFold(folder, "Junk") {
			tool("spam", i18n.T(lang, "Junk"), "!", paneVals("to", "Junk"), `data-mail-move="Junk"`, false, "")
		}
		// Move: a menu of the folders this message can go to.
		// A menu's key is on its summary, which is how the keys find it
		// (admin-os-mail.js), in whatever language its words are.
		c.WriteString(`<details class="sa-pop"><summary class="mx-tool mx-tool--word" title="` + esc(i18n.T(lang, "Move to… (v)")) + `" aria-label="` + esc(i18n.T(lang, "Move to")) + `" aria-keyshortcuts="v">` + saIcon("move") + words(i18n.T(lang, "Move")) + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
		for _, f := range a.moveTargets(rd, folder) {
			attrs := `data-mail-move="` + esc(f) + `"`
			if pane {
				attrs = hxPost + paneVals("to", f)
			}
			c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" ` + attrs + `>` + saIcon(mailFolderIcon(f)) + `<span class="sa-menu__text">` + esc(mailFolderLabel(lang, f)) + `</span></button>`)
		}
		c.WriteString(`</div></details>`)
		if pane && cur.MessageID != "" {
			allLabels, _ := a.vayuMail.Labels(rd)
			c.WriteString(labelMenu(rd.Lang(), user, folder, id, allLabels, msgLabels))
		}
	}
	c.WriteString(`<span class="mx-rtools__sep" aria-hidden="true"></span>`)
	if pane && received && !readOnly && !strings.EqualFold(folder, "Snoozed") {
		// "Later" (+4h) existed in the engine but had no button, so the
		// fastest snooze the product could offer was "tomorrow".
		c.WriteString(`<details class="sa-pop"><summary class="mx-tool mx-tool--word" title="` + esc(i18n.T(lang, "Snooze (b)")) + `" aria-label="` + esc(i18n.T(lang, "Snooze")) + `" aria-keyshortcuts="b">` + saIcon("timer") + words(i18n.T(lang, "Snooze")) + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
		for _, s := range [][2]string{{"later", i18n.T(lang, "Later today")}, {"tomorrow", i18n.T(lang, "Tomorrow, 8:00")}, {"nextweek", i18n.T(lang, "Next week, Monday 8:00")}} {
			c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem"` + hxPost + paneVals("snooze", s[0]) + `><span class="sa-menu__text">` + esc(s[1]) + `</span></button>`)
		}
		c.WriteString(`</div></details>`)
	}
	pinArgs, pinPage, pinLabel := paneVals("pin", "1"), `data-mail-pin="1"`, i18n.T(lang, "Pin")
	if cur.Flagged {
		pinArgs, pinPage, pinLabel = paneVals("pin", "0"), `data-mail-pin="0"`, i18n.T(lang, "Unpin")
	}
	tool("pin", pinLabel, "s", pinArgs, pinPage, false, "")
	c.WriteString(`</div><span class="mx-rtools__fill"></span><div class="mx-rtools__group">`)
	if pos >= 0 {
		c.WriteString(`<span class="mx-rtools__pos">` + esc(i18n.T(lang, "{n} of {total}", "n", pos+1, "total", len(msgs))) + `</span>`)
	}
	nav := func(mid, icon, label, key, travel string) {
		if mid == "" {
			c.WriteString(`<span class="mx-tool" aria-disabled="true" aria-label="` + esc(label) + `">` + saIcon(icon) + `</span>`)
			return
		}
		if pane {
			c.WriteString(`<button type="button" class="mx-tool" data-mx-travel="` + travel + `" hx-get="` + esc(msgURL(mid)) + `&amp;pane=1" hx-target="#vm-readpane" hx-swap="innerHTML" title="` + esc(label) + ` (` + key + `)" aria-label="` + esc(label) + `" aria-keyshortcuts="` + key + `">` + saIcon(icon) + `</button>`)
			return
		}
		c.WriteString(`<a class="mx-tool" href="` + esc(msgURL(mid)) + `" title="` + esc(label) + ` (` + key + `)" aria-label="` + esc(label) + `" aria-keyshortcuts="` + key + `">` + saIcon(icon) + `</a>`)
	}
	nav(newerID, "chev-u", i18n.T(lang, "Newer message"), "k", "newer")
	nav(olderID, "chev-d", i18n.T(lang, "Older message"), "j", "older")

	if pane {
		// Full width (pipeline 3y): the message is pushed over the list and
		// takes its place, as compose's Expand does, and the bar's back button
		// (shown whenever a message covers the list) returns to the columns.
		// Hidden whenever the message already covers the list (admin-os-mail.js).
		c.WriteString(`<button type="button" class="mx-tool mx-widen" data-mx-widen title="` + esc(i18n.T(lang, "Full width (w)")) + `" aria-label="` + esc(i18n.T(lang, "Full width")) + `"` + mailCmd(i18n.T(lang, "Full width"), here, "w") + `>` + saIcon("expand") + `</button>`)
	}

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
		return `<a class="sa-menu__item" role="menuitem" href="` + esc(u) + `"` + hx + `><span class="sa-menu__text">` + esc(label) + `</span></a>`
	}
	hasHTML := strings.TrimSpace(pm.HTML) != ""
	c.WriteString(`<details class="sa-pop"><summary class="mx-tool" title="` + esc(i18n.T(lang, "More")) + `" aria-label="` + esc(i18n.T(lang, "More")) + `">` + saIcon("more") + `</summary><div class="sa-pop__panel sa-menu" role="menu">`)
	if hasHTML && !v.HTML && strings.TrimSpace(pm.Text) != "" {
		c.WriteString(view(i18n.T(lang, "Show as the sender styled it"), true, false))
	}
	if hasHTML && v.HTML {
		c.WriteString(view(i18n.T(lang, "Show as plain text"), false, false))
	}
	if hasHTML && !v.Images {
		c.WriteString(view(i18n.T(lang, "Load images"), true, true))
	}
	if received {
		attrs := `data-mail-mark="unread"`
		if pane {
			attrs = hxPost + paneVals("mark", "unread")
		}
		c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" ` + attrs + `><span class="sa-menu__text">` + esc(i18n.T(lang, "Mark unread")) + `</span><kbd class="sa-menu__key">⇧U</kbd></button>`)
	}
	c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" data-vm-print><span class="sa-menu__text">` + esc(i18n.T(lang, "Print")) + `</span></button>`)
	if !readOnly && received && fromAddr != "" {
		// Block (blocked.go): what it does is said before it is done.
		c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" hx-post="/os/vayumail/blocked/action" hx-swap="none"` +
			hxVals("user", user, "action", "block", "sender", fromAddr) +
			` hx-confirm="` + esc(i18n.T(lang, "Block {sender}? Their mail will be refused, or put straight in Trash.", "sender", fromAddr)) + `"><span class="sa-menu__text">` + esc(i18n.T(lang, "Block {sender}", "sender", fromAddr)) + `</span></button>`)
	}
	if !readOnly && !strings.EqualFold(folder, "Trash") {
		attrs := `data-mail-delete`
		if pane {
			attrs = hxPost + paneVals("delete", "1") + ` hx-confirm="` + esc(i18n.T(lang, "Delete this message for good?")) + `"`
		}
		c.WriteString(`<button type="button" class="sa-menu__item sa-menu__item--danger" role="menuitem" ` + attrs + `><span class="sa-menu__text">` + esc(i18n.T(lang, "Delete for good")) + `</span></button>`)
	}
	if pane {
		c.WriteString(`<button type="button" class="sa-menu__item" role="menuitem" hx-get="/os/vayumail/inbox/readpane" hx-target="#vm-readpane" hx-swap="innerHTML"><span class="sa-menu__text">` + esc(i18n.T(lang, "Close")) + `</span></button>`)
	} else {
		c.WriteString(`<a class="sa-menu__item" role="menuitem" href="/os/vayumail/inbox?user=` + qparam(user) + `&amp;folder=` + qparam(folder) + `"><span class="sa-menu__text">` + esc(i18n.T(lang, "Back to {folder}", "folder", mailFolderLabel(lang, folder))) + `</span></a>`)
	}
	c.WriteString(`</div></details></div></div>`)

	// ── The message itself, scrolling under the tools.
	c.WriteString(`<div class="mx-rbody">`)
	n := len(convo) + 1
	c.WriteString(`<header><h2 class="mx-subject">` + esc(subj) + `</h2><p class="mx-subline">` + esc(mailFolderLabel(lang, folder)))
	if n > 1 {
		c.WriteString(` <span aria-hidden="true">·</span> ` + esc(i18n.T(lang, "{n, plural, one {# message} other {# messages}}", "n", n)))
	}
	c.WriteString(`</p>` + readerLabels(user, msgLabels) + `</header>`)

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
				`<span class="mx-earlier__pv">` + esc(m.msg.Preview) + `</span><time class="mx-earlier__time">` + esc(mailListTime(rd.Lang(), m.msg.Date, time.Now())) + `</time></summary>` +
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
	if to := mailRecipientsLine(lang, pm.To, pm.Cc, mbox); to != "" {
		c.WriteString(`<div class="mx-msg__to">` + esc(to) + `</div>`)
	}
	c.WriteString(`</div>`)
	if !cur.Date.IsZero() {
		c.WriteString(`<time class="mx-msg__time" datetime="` + cur.Date.UTC().Format(time.RFC3339) + `" title="` + esc(config.FormatSiteStamp(cur.Date)) + `">` + esc(mailReaderTime(rd.Lang(), cur.Date, time.Now())) + `</time>`)
	}
	c.WriteString(`</div>`)
	// The seal comes from what was verified, read from the message as it was
	// delivered (decrypting it for display drops the signature).
	if stored, err := a.vayuMail.ReadFolderMessageStored(rd, folder, id); err == nil {
		var checker sealChecker
		if a.vayuPGP != nil {
			checker = sealKeys{pgp: a.vayuPGP, known: a.vayuMail.KnownKeys(rd)}
		}
		if s := mailSealFor(nil, checker, mbox, stored, fromAddr); s.Text != "" {
			icon := "lock"
			if !strings.Contains(s.Text, "ncrypted") {
				icon = "check-c"
			}
			c.WriteString(`<p class="mx-seal mx-seal--` + map[string]string{"ok": "ok", "danger": "danger", "": "plain"}[s.Tone] + `">` + saIcon(icon) + `<span>` + esc(s.Text) + `</span></p>`)
		}
		sanitized := ""
		if strings.TrimSpace(pm.HTML) != "" {
			sanitized = mailHTMLPolicyImages.Sanitize(pm.HTML)
		}
		trust := mailTrustFor(lang, a.vayuMail.CheckedDomain(stored), sanitized, pm.From, !a.vayuMail.IsContact(rd, fromAddr))
		if !received {
			trust.Warning = "" // mail this mailbox wrote warns no one
		}
		c.WriteString(mailTrustLine(lang, trust))
		if pane {
			c.WriteString(a.modelHelpBar(context.Background(), rd, folder, id, cur, raw, pm.Subject))
		}
		// What this message did not reach, from the queue or a report.
		if bounces, _ := a.vayuMail.Bounces(rd, cur.MessageID); len(bounces) > 0 {
			c.WriteString(`<ul class="mx-bounces">`)
			for _, b := range bounces {
				c.WriteString(`<li>` + saIcon("alert") + `<span><strong>` + esc(i18n.T(lang, "Not delivered to {recipient}.", "recipient", b.Recipient)) + `</strong> ` + esc(b.Reason) + ` <span class="mx-bounces__code">` + esc(b.Status) + `</span></span></li>`)
			}
			c.WriteString(`</ul>`)
		}
		if o := vmail.ParseListUnsubscribe(stored); o.Any() && received && !readOnly {
			c.WriteString(mailUnsubscribeLine(rd.Lang(), user, folder, id, o))
		}
	}
	if received && v.Places.Calendar {
		home := v.Home
		if home == nil {
			home = config.SiteLocation()
		}
		c.WriteString(a.inviteCard(rd, folder, id, raw, home, ""))
	}
	c.WriteString(`<div class="mx-msg__body">` + a.mailBodyHTML(lang, pm, raw, v) + `</div>`)

	// Attachments as cards: a type tile, the name, the size, and download.
	if len(pm.Attachments) > 0 {
		// Pictures shown in place, from the attachment endpoint, which serves
		// a picture inline only when its bytes are one.
		var thumbs strings.Builder
		for _, att := range pm.Attachments {
			if picture, ok := mailPreviewable(att.ContentType); ok && picture && att.Size <= 10<<20 {
				u := "/os/vayumail/attachment?" + q + "&idx=" + itoaSafe(att.Index) + "&inline=1"
				thumbs.WriteString(`<a class="mx-preview" href="` + esc(u) + `" target="_blank" rel="noopener"><img src="` + esc(u) + `" alt="` + esc(vmail.PlainName(att.Filename)) + `" loading="lazy"></a>`)
			}
		}
		if thumbs.Len() > 0 {
			c.WriteString(`<div class="mx-previews">` + thumbs.String() + `</div>`)
		}
		c.WriteString(`<ul class="mx-files" aria-label="` + esc(i18n.T(lang, "Attachments")) + `">`)
		for _, att := range pm.Attachments {
			dl := "/os/vayumail/attachment?" + q + "&idx=" + itoaSafe(att.Index)
			// The tile carries a ring the download fills and the tick it settles
			// to (admin-os-mail.js, Mail plan §8 item 7).
			c.WriteString(`<li><a class="mx-file" href="` + esc(dl) + `" download data-mx-file><span class="mx-file__type" aria-hidden="true">` + esc(mailFileType(att.Filename, att.ContentType)) +
				`<svg class="mx-file__ring" viewBox="0 0 36 36"><circle cx="18" cy="18" r="16" pathLength="100"/></svg><span class="mx-file__tick">` + saIcon("check") + `</span></span>` +
				`<span class="mx-file__meta"><span class="mx-file__name">` + esc(vmail.PlainName(att.Filename)) + `</span><span class="mx-file__size">` + esc(humanBytes(att.Size)) + `</span></span>` +
				`<span class="mx-file__dl" aria-hidden="true">` + saIcon("download") + `</span><span class="vp-sr-only">` + esc(i18n.T(lang, "Download")) + `</span></a>`)
			if _, ok := mailPreviewable(att.ContentType); ok {
				c.WriteString(`<a class="mx-file__open" href="` + esc(dl+"&inline=1") + `" target="_blank" rel="noopener">` + esc(i18n.T(lang, "Open")) + `<span class="vp-sr-only"> ` + esc(vmail.PlainName(att.Filename)) + `</span></a>`)
			}
			// Into the holder's own Drive (vayuos_mail_drive.go): for the
			// mailbox's holder, not an administrator reading it, and where
			// the session has a Drive. The button is replaced by what happened.
			if rd.IsOwner() && v.Places.Drive {
				c.WriteString(`<button type="button" class="mx-file__open" hx-post="/os/vayumail/drive/save"` + hxVals("folder", folder, "id", id, "idx", itoaSafe(att.Index)) +
					` hx-swap="outerHTML">` + esc(i18n.T(lang, "Save to Drive")) + `<span class="vp-sr-only"> ` + esc(vmail.PlainName(att.Filename)) + `</span></button>`)
			}
			c.WriteString(`</li>`)
		}
		c.WriteString(`</ul>`)
	}
	// The message as it arrived, for the rare day it is needed.
	c.WriteString(`<details class="mx-source"><summary>` + esc(i18n.T(lang, "Show the original")) + `</summary><pre class="vm-pre vm-raw">` + esc(string(raw)) + `</pre></details>`)
	c.WriteString(`</article></div>`)

	// A phone's message screen has its own bar in place of the tab bar
	// (render 05): what a message most needs, at the thumb.
	if pane && !readOnly {
		c.WriteString(`<nav class="mx-phonebar" aria-label="` + esc(i18n.T(lang, "Message")) + `">`)
		if strings.EqualFold(folder, "Junk") {
			c.WriteString(`<button type="button" class="mx-tool"` + hxPost + paneVals("notjunk", "1") + ` aria-label="` + esc(i18n.T(lang, "Not junk")) + `">` + saIcon("inbox") + `</button>`)
		}
		if received && !strings.EqualFold(folder, "Archive") {
			c.WriteString(`<button type="button" class="mx-tool"` + hxPost + paneVals("to", "Archive") + ` aria-label="` + esc(i18n.T(lang, "Archive")) + `">` + saIcon("archive") + `</button>`)
		}
		if !strings.EqualFold(folder, "Trash") {
			c.WriteString(`<button type="button" class="mx-tool"` + hxPost + paneVals("to", "Trash") + ` aria-label="` + esc(i18n.T(lang, "Move to Trash")) + `">` + saIcon("trash") + `</button>`)
		}
		c.WriteString(`<button type="button" class="mx-tool" data-mx-menu="v" aria-label="` + esc(i18n.T(lang, "Move to")) + `">` + saIcon("move") + `</button>`)
		if received {
			c.WriteString(`<a class="mx-tool" href="/os/vayumail/compose?reply=1&amp;` + esc(q) + `" aria-label="` + esc(i18n.T(lang, "Reply")) + `">` + saIcon("reply") + `</a>`)
		}
		c.WriteString(`<a class="mx-tool" href="/os/vayumail/compose?user=` + qparam(user) + `" aria-label="` + esc(i18n.T(lang, "Write a message")) + `">` + saIcon("pencil") + `</a></nav>`)
	}

	// Reply, docked: a field that opens the reply with nothing lost.
	if !readOnly && received && fromAddr != "" {
		first := strings.Fields(who)
		name := who
		if len(first) > 0 {
			name = first[0]
		}
		// A field, not a link (Mail plan §3.3, pipeline 3z): what is typed in
		// it becomes the reply, carried into the compose sheet it opens.
		c.WriteString(`<div class="mx-dock"><input class="mx-dock__field" type="text" autocomplete="off" data-mx-quick-reply data-href="/os/vayumail/compose?reply=1&amp;` + esc(q) + `" placeholder="` + esc(i18n.T(lang, "Reply to {name}…", "name", name)) + `" aria-label="` + esc(i18n.T(lang, "Reply to {name}", "name", name)) + `">`)
		if a.vayuPGP != nil {
			if _, err := a.vayuPGP.GetPublicKey(fromAddr); err == nil {
				c.WriteString(`<span class="mx-dock__enc">` + saIcon("lock") + ` ` + esc(i18n.T(lang, "Encrypted")) + `</span>`)
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
func (a *App) mailBodyHTML(lang string, pm vmail.ParsedMessage, raw []byte, v readerView) string {
	hasHTML := strings.TrimSpace(pm.HTML) != ""
	note := `<p class="mx-images-off">` + esc(i18n.T(lang, "Pictures stay off until you ask. They load through this server, so the sender learns that the message was opened, but not your address or device.")) + `</p>`
	switch {
	case hasHTML && v.HTML:
		if v.Images {
			return `<div class="vm-html">` + proxyImageSources(mailHTMLPolicyImages.Sanitize(pm.HTML)) + `</div>`
		}
		return note + `<div class="vm-html">` + mailHTMLNoImages(pm.HTML) + `</div>`
	case strings.TrimSpace(pm.Text) != "":
		main, quoted := splitQuoted(pm.Text)
		out := `<div class="mx-text">` + esc(strings.TrimSpace(main)) + `</div>`
		if quoted != "" {
			out += `<details class="mx-quote"><summary>` + esc(i18n.T(lang, "Show the quoted message")) + `</summary><div class="mx-text mx-text--quoted">` + esc(quoted) + `</div></details>`
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
func mailRecipientsLine(lang, to, cc, me string) string {
	name := func(s string) string {
		n, addr := mailParseFrom(s)
		if strings.EqualFold(addr, me) {
			return i18n.T(lang, "me")
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
		line = i18n.T(lang, "to {people}", "people", strings.Join(t, ", "))
	}
	if c := list(cc); len(c) > 0 {
		if line != "" {
			line += ", "
		}
		line += i18n.T(lang, "cc {people}", "people", strings.Join(c, ", "))
	}
	return line
}

// mailReaderTime is a message's time in full enough words to stand alone:
// "Today, 14:20", "Yesterday, 09:05", "Thu 2 Oct, 14:20".
func mailReaderTime(lang string, t, now time.Time) string {
	switch mailListWhen(t, now) {
	case whenToday:
		return i18n.T(lang, "Today, {time}", "time", config.FormatSite(t, "15:04"))
	case whenYesterday:
		return i18n.T(lang, "Yesterday, {time}", "time", config.FormatSite(t, "15:04"))
	case whenThisWeek, whenThisYear:
		return i18n.Date(lang, config.InSite(t), i18n.WeekdayDayMonthAt)
	}
	return i18n.Date(lang, config.InSite(t), i18n.DayMonthYearShortAt)
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
		writeOSFragment(w, `<p class="mx-empty">`+esc(i18n.T(rd.Lang(), "This message could not be read."))+`</p>`)
		return
	}
	writeOSFragment(w, a.mailBodyHTML(rd.Lang(), vmail.ParseMessage(raw), raw, readerView{Pane: true}))
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

// handleVayuOSMailLayout keeps how the signed-in person opens a message
// (Mail's Reading choice): "full" over the list at full width, anything else
// beside it. Only their own account is written, and a session with none (the
// API key) is refused rather than given somewhere to keep it.
func (a *App) handleVayuOSMailLayout(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil || a.userStore == nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Reading is kept on a user account, and this session has none."), "")
		return
	}
	if err := a.userStore.SetMailLayout(r.Context(), u.ID, strings.TrimSpace(r.FormValue("layout"))); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-failed", i18n.T(i18n.Lang(r.Context()), "Reading could not be saved."), "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
