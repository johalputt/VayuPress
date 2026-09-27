// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"

	"net/http"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// mailInstallCommand is the installer's documented one-line install
// (docs/INSTALLATION.md). A DOMAIN given to it replaces the one an earlier run
// wrote to /etc/vayupress/env, so on an installed server it is also how the
// domain is changed.
const mailInstallCommand = "curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/deploy-vayupress.sh | sudo DOMAIN=example.com EMAIL=you@example.com bash"

// mailRunning reports whether Mail is running. It runs for the domain
// VayuPress is installed for, and not on an install that answers only on
// localhost (vayuos.go, where the engine is configured).
func (a *App) mailRunning() bool {
	return a.vayuMail != nil && a.vayuMail.Config().Enabled
}

// writeMailSetup answers any Mail page while Mail is not running, with the one
// page that says what Mail is and what it needs. Each page used to say it in a
// sentence of its own; one of them sent the operator to a "first-boot wizard"
// that does not exist. Nothing in the console can set the domain: the
// installer writes it, so that step shows the installer's command.
func (a *App) writeMailSetup(w http.ResponseWriter, r *http.Request, title string) {
	body := ui.Setup(ui.SetupPage{
		Icon:  "mail",
		Title: "Mail isn't set up yet",
		What:  "Your own mail server for this site: addresses for you and your team, encrypted with PGP, readable in any mail app and on your phone.",
		Steps: []ui.SetupStep{
			{Title: "A domain", Detail: "This install answers only on localhost. Mail runs for the domain VayuPress is installed for, and the installer sets it; put yours in place of example.com:",
				Command: mailInstallCommand},
			{Title: "Four DNS records", Detail: "MX, SPF, DKIM and DMARC, published at your DNS provider. Mail lists their values, ready to copy, once it runs."},
			{Title: "Port 25 open", Detail: "Mail from other servers arrives on it. Many providers block it until asked."},
		},
		Action: copyCommandButton("Copy the install command", mailInstallCommand),
		More:   `<a class="btn btn--ghost" href="/docs/installation">Installation guide</a>`,
	})
	writeOSHTML(w, r, adminOSLayout(render.CSPNonce(r), title, "vayuos", a.getOSSettings(r.Context()), body))
}

// copyCommandButton is a setup page's primary button when the next step is a
// command: the console cannot run it (the installer, say), so the one thing it
// can hand over is the command, copied.
func copyCommandButton(label, command string) ui.HTML {
	return ui.HTML(`<button type="button" class="btn btn--primary" data-copy="` + string(ui.Text(command)) + `">` + string(ui.Text(label)) + `</button>`)
}

// talkOnCommand turns Talk back on where VAYUOS_TALK=off turned it off: the
// line goes from the env file the installer writes, and the service restarts
// to read it.
const talkOnCommand = "sudo sed -i '/^VAYUOS_TALK=/d' /etc/vayupress/env && sudo systemctl restart vayupress"

// writeTalkSetup answers Talk until it has an identity to chat as. It needs
// Mail running, not to be turned off, and a mailbox; the steps already done
// are ticked, and the button does the first one left.
func (a *App) writeTalkSetup(w http.ResponseWriter, r *http.Request) {
	mail := a.mailRunning()
	off := strings.EqualFold(config.EnvOr("VAYUOS_TALK", "on"), "off")
	steps := []ui.SetupStep{{Title: "Mail running", Done: mail,
		Detail: "Talk runs beside Mail, for the same domain."}}
	if !mail {
		steps[0].Command = mailInstallCommand
	}
	if off {
		steps = append(steps, ui.SetupStep{Title: "Talk turned on",
			Detail: "VAYUOS_TALK=off in /etc/vayupress/env turns it off. Removing the line and restarting turns it on:", Command: talkOnCommand})
	}
	mailbox := ui.SetupStep{Title: "A mailbox", Detail: "Your chat identity is one of your mailboxes, so people reach you at an address they already know."}
	if mail {
		mailbox.Href = "/os/vayumail/accounts"
	}
	steps = append(steps, mailbox)

	var action ui.HTML
	switch {
	case !mail:
		action = copyCommandButton("Copy the install command", mailInstallCommand)
	case off:
		action = copyCommandButton("Copy the command", talkOnCommand)
	default:
		action = `<a class="btn btn--primary" href="/os/vayumail/accounts">Create a mailbox</a>`
	}
	body := ui.Setup(ui.SetupPage{
		Icon:  "talk",
		Title: "Talk isn't set up yet",
		What:  "Private chat for you and the people you write to: end-to-end encrypted, gone a set time after it is read, in the browser and in the app.",
		Steps: steps, Action: action,
	})
	writeOSHTML(w, r, adminOSLayout(render.CSPNonce(r), "VayuTalk", "talk", a.getOSSettings(r.Context()), body))
}
