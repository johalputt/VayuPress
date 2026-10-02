// SPDX-License-Identifier: Apache-2.0

// mail-fixture seeds Maildir trees for a browser-test install with Mail on.
//
// Usage: mail-fixture <maildir-root> <domain>
//
// The e2e suite never saw Mail running: its install is "localhost", for which
// Mail stays off, so every Mail spec could test only the "set it up" page. This
// writes what a mail install holds, the way delivery writes it (new/ for
// unread, cur/ with :2,<flags> for seen and pinned, mtime at the message's
// date), so a second install started on it has mailboxes to show:
//
//   - ankush: every case the list draws (pinned, unread, a conversation whose
//     middle is in Sent, HTML only, quoted-printable, PGP/MIME encrypted,
//     signed, an attachment, a 60-character sender), then 200 older
//     messages so the list scrolls;
//   - priya, with one unread message;
//   - a mailbox whose address is long, for the wrapping checks.
//
// Everything is fixed (ids, names, ages), so a screenshot of it is repeatable.
package main

import (
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type msg struct {
	from, to, subject, body string
	ageHours                float64
	ctype, cte              string
	id, reply               string
	refs                    []string
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mail-fixture <maildir-root> <domain>")
		os.Exit(2)
	}
	root, dom := os.Args[1], os.Args[2]
	now := time.Now()
	n := 0
	write := func(user, folder string, m msg, seen, pinned bool) {
		dir := filepath.Join(root, dom, user)
		if folder != "Inbox" {
			dir = filepath.Join(dir, "."+folder)
		}
		for _, sub := range []string{"tmp", "new", "cur"} {
			must(os.MkdirAll(filepath.Join(dir, sub), 0o700))
		}
		at := now.Add(-time.Duration(m.ageHours * float64(time.Hour)))
		n++
		name := fmt.Sprintf("%d.M%dP1.fixture", at.Unix(), n)
		sub := "new"
		if seen || pinned {
			flags := ""
			if pinned {
				flags += "F"
			}
			if seen {
				flags += "S"
			}
			sub, name = "cur", name+":2,"+flags
		}
		ctype := m.ctype
		if ctype == "" {
			ctype = "text/plain; charset=utf-8"
		}
		id := m.id
		if id == "" {
			id = fmt.Sprintf("m%d", n)
		}
		h := []string{"From: " + m.from, "To: " + m.to, "Subject: " + m.subject,
			"Date: " + at.Format(time.RFC1123Z), "Message-Id: <" + id + "@fixture>",
			"MIME-Version: 1.0", "Content-Type: " + ctype}
		if m.cte != "" {
			h = append(h, "Content-Transfer-Encoding: "+m.cte)
		}
		if m.reply != "" {
			var refs []string
			for _, r := range append(m.refs, m.reply) {
				refs = append(refs, "<"+r+"@fixture>")
			}
			h = append(h, "In-Reply-To: <"+m.reply+"@fixture>", "References: "+strings.Join(refs, " "))
		}
		raw := strings.ReplaceAll(strings.Join(h, "\n")+"\n\n"+m.body+"\n", "\n", "\r\n")
		p := filepath.Join(dir, sub, name)
		must(os.WriteFile(p, []byte(raw), 0o600))
		must(os.Chtimes(p, at, at))
	}

	me := (&mail.Address{Name: "Ankush Johal", Address: "ankush@" + dom}).String()
	const u = "ankush"
	write(u, "Inbox", msg{from: "Let's Encrypt <expiry@letsencrypt.org>", to: me, subject: "Your certificate expiry notices are now on",
		body: "We'll write 20 days before any certificate for this site expires, and again at 7 days.", ageHours: 20 * 24}, true, true)
	// A conversation: they write, I reply (in Sent), they reply.
	write(u, "Inbox", msg{from: "Priya Raman <priya@halcyon.dev>", to: me, subject: "the migration window on Saturday",
		body: "We need to move the database this month. Can we take Saturday night?", ageHours: 30, id: "mig1"}, true, false)
	write(u, "Sent", msg{from: me, to: "Priya Raman <priya@halcyon.dev>", subject: "Re: the migration window on Saturday",
		body: "Friday works too, but Saturday night is quieter for the newsletter.", ageHours: 26, id: "mig2", reply: "mig1"}, true, false)
	write(u, "Inbox", msg{from: "Priya Raman <priya@halcyon.dev>", to: me, subject: "Re: the migration window on Saturday", ageHours: 0.6, id: "mig3", reply: "mig2", refs: []string{"mig1"},
		ctype: `multipart/mixed; boundary="b"`,
		body: "--b\nContent-Type: text/plain; charset=utf-8\n\nSaturday 02:00 to 04:00 IST works for us. Two things before we commit to it: can the queue hold inbound mail for the whole window?\n" +
			"--b\nContent-Type: application/pdf; name=\"migration-runbook-v3.pdf\"\nContent-Disposition: attachment; filename=\"migration-runbook-v3.pdf\"\nContent-Transfer-Encoding: base64\n\nJVBERi0xLjQK\n--b--"}, false, false)
	write(u, "Inbox", msg{from: "GitHub <noreply@github.com>", to: me, subject: "[VayuPress] Release v3.17.94 published", ageHours: 2, ctype: "text/html; charset=utf-8",
		body: "<html><head><style>p{color:#333}</style></head><body><p>The release workflow completed: <b>14 assets</b> signed and attested.</p><p>The update check will offer it within the hour.</p></body></html>"}, false, false)
	write(u, "Inbox", msg{from: "Mehul Shah <mehul@shah.example>", to: me, subject: "The Shield posture numbers", ageHours: 3.5, cte: "quoted-printable",
		body: "Loved the post. One question about the volumetric line =E2=80=94 is that per host or across the whole install? I'd like to quote it."}, true, false)
	write(u, "Inbox", msg{from: "Rohan Kapoor <rohan@kapoor.example>", to: me, subject: "Keys for the backup bucket", ageHours: 5,
		ctype: `multipart/encrypted; protocol="application/pgp-encrypted"; boundary="e"`,
		body:  "--e\nContent-Type: application/pgp-encrypted\n\nVersion: 1\n--e\nContent-Type: application/octet-stream\n\n-----BEGIN PGP MESSAGE-----\nhQEMA0secret\n-----END PGP MESSAGE-----\n--e--"}, true, false)
	write(u, "Inbox", msg{from: "Ananya Iyer <ananya@iyer.example>", to: me, subject: "Newsletter draft, October", ageHours: 26,
		ctype: `multipart/signed; protocol="application/pgp-signature"; micalg=pgp-sha256; boundary="s"`,
		body:  "--s\nContent-Type: text/plain\n\nAttached the outline. I kept it to four sections so the issue stays short enough to read on a phone.\n--s\nContent-Type: application/pgp-signature\n\n-----BEGIN PGP SIGNATURE-----\niQE\n-----END PGP SIGNATURE-----\n--s--"}, true, false)
	write(u, "Inbox", msg{from: "<notifications-for-the-quarterly-billing-run@accounts.example.com>", to: me, subject: "Invoice #2026-091", ageHours: 50,
		body: "Hi Ankush, please find this month's hosting invoice attached. Nothing has changed from last month."}, true, false)
	names := []string{"Kabir Sethi", "Meera Nair", "Arjun Rao", "Sara Thomas", "Dev Malhotra", "Isha Kulkarni", "Noah Fischer", "Lena Vogt"}
	for i := 0; i < 200; i++ {
		name := names[i%len(names)]
		addr := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@example.org"
		write(u, "Inbox", msg{from: name + " <" + addr + ">", to: me, subject: fmt.Sprintf("Notes from week %d", 40-i/7),
			body: fmt.Sprintf("Short notes from the week: what shipped, what slipped, and what is next. Item %d of the running list.", i), ageHours: float64(72 + i*9)}, i%5 != 0, false)
	}
	for _, f := range []string{"Drafts", "Archive", "Junk", "Trash", "Snoozed"} {
		must(os.MkdirAll(filepath.Join(root, dom, u, "."+f, "cur"), 0o700))
	}
	write("priya", "Inbox", msg{from: me, to: "priya@" + dom, subject: "Welcome", body: "Your mailbox is ready.", ageHours: 4}, false, false)
	long := "a-very-long-mailbox-name-for-the-wrapping-test"
	write(long, "Inbox", msg{from: me, to: long + "@" + dom, subject: "Hello", body: "A mailbox whose address is long.", ageHours: 4}, true, false)
	fmt.Printf("seeded %d messages in %s\n", n, filepath.Join(root, dom))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
