// SPDX-License-Identifier: Apache-2.0

package main

// handlers_mail_device_reset.go — trusted-device recovery (ADR-0144 Phase 2).
//
// A holder whose phone still syncs has an app password that works, and that
// credential already proves what a recovery factor needs to prove: at enrolment
// time they controlled the mailbox, and the operator approved the device. So the
// app can set a new mailbox password without knowing the old one.
//
// This is the only factor that needs no advance planning beyond having used the
// app, which makes it the one most people will actually have when they need it.
//
// It runs the same pipeline as every other reset, which means it revokes EVERY
// app password — including the one that authorised it. That is deliberate: the
// server cannot tell a holder's device from a thief's, only that the credential
// is valid, so preserving the initiating device would hand an attacker a way to
// reset the password and keep sole access. The app re-enrols afterwards like any
// other client.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// deviceResetByIP bounds guessing at app passwords through this endpoint. It is
// separate from the app's login throttle so a burst here cannot be used to lock
// legitimate sign-ins out of their own budget.
var deviceResetByIP = newIngestLimiter(10, time.Hour)

// deviceResetByAddress bounds the SAME endpoint by the mailbox being attacked.
//
// AUDIT FINDING (Section 1). deviceResetByIP was the only budget here, and past
// it VerifyApprovedDevice runs auth.VerifySecretArgon2id over every approved
// credential for the address — up to twenty sequential 64 MiB derivations for
// one unauthenticated request. While the IP key was spoofable that bound nothing
// at all; even now it is the wrong axis on its own, because a distributed caller
// simply brings more sources. Keyed on the submitted address, this one holds
// however many hosts the caller has, and it is the axis the sibling recovery
// flow already uses (recoveryByAddress, same 3/hour).
var deviceResetByAddress = newIngestLimiter(3, time.Hour)

// handleMailDeviceReset lets an enrolled device set a new mailbox password.
//
// POST /api/v1/members/vayumail-device-reset
//
//	{"email":"…","app_password":"…","new_password":"…"}
func (a *App) handleMailDeviceReset(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	var in struct {
		Email       string `json:"email"`
		AppPassword string `json:"app_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&in); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "invalid_json", "could not read the request", "")
		return
	}
	addr := strings.ToLower(strings.TrimSpace(in.Email))
	ip := clientIPForContact(r)

	// One uniform failure for every rejection below — wrong mailbox, wrong
	// credential, blocked device, over budget. Distinguishing them would turn this
	// into an oracle for which mailboxes exist and which app passwords are live.
	deny := func(reason string) {
		dbpkg.AuditLog("vayumail.recovery.device_rejected", "device:"+ip, addr, reason)
		writeAPIError(w, r, http.StatusUnauthorized, "unauthorized",
			"That mailbox and app password were not accepted.", "")
	}

	if !deviceResetByIP.allow(ip) || !deviceResetByAddress.allow(addr) {
		deny("rate-limited")
		return
	}
	if addr == "" || in.AppPassword == "" {
		deny("missing fields")
		return
	}
	// The password rule is checked BEFORE the credential so a valid device cannot
	// be used to probe strength requirements, and so a caller learns nothing from
	// the ordering.
	if len([]rune(in.NewPassword)) < minMailPasswordLen {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error",
			"Choose a password of at least 8 characters.", "")
		return
	}

	accts := a.vayuMail.Accounts()
	credID, ok := accts.VerifyApprovedDevice(r.Context(), addr, in.AppPassword)
	if !ok {
		deny("no approved device matched")
		return
	}

	deps, depsOK := a.mailResetDepsFor()
	if !depsOK {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "VayuMail is not running", "")
		return
	}
	out, err := applyMailPasswordReset(r.Context(), deps, addr, in.NewPassword,
		mailResetByDevice, "device:"+ip)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "reset-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayumail.recovery.device_accepted", "device:"+ip, addr,
		"credential_id="+itoa64(credID))

	// The response tells the app what it must now do. Reporting the revoked count
	// matters on this path in particular: the holder is looking at one device, and
	// a number higher than they expect is how they learn another one existed.
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"reset":                 true,
		"app_passwords_revoked": out.AppPasswordsRevoked,
		"sessions_revoked":      out.SessionsRevoked,
		"reenrol_required":      true,
		"detail": "Your mailbox password has been changed. Every connected app, including this one, " +
			"has been signed out — set this device up again with the new password.",
	})
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
