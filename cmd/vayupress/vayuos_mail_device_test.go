// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_device_test.go — device approval for VayuMail (ADR-0129).
//
// The model under test: a NEW device registers with the mailbox password and
// receives a pending device credential; nothing syncs to it (and the private
// key stays locked) until the operator approves it from the 2FA-protected web
// console; the raw mailbox password never syncs mail while approval is
// required, but keeps working for the member HTTP bootstrap endpoints.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// postMemberJSON drives one of the member device endpoints with a JSON body,
// as the VayuMail-Mobile app posts it.
func postMemberJSON(h http.HandlerFunc, path string, body map[string]string) *httptest.ResponseRecorder {
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// registerDevice enrols a device through the member endpoint and decodes the
// response.
func registerDevice(t *testing.T, a *App, email, password, name, platform string) (rec *httptest.ResponseRecorder, deviceID, devicePassword, status string) {
	t.Helper()
	rec = postMemberJSON(a.handleMemberVayuMailDeviceRegister, "/api/v1/members/vayumail-device-register",
		map[string]string{"email": email, "password": password, "device_name": name, "platform": platform})
	if rec.Code != http.StatusOK {
		return rec, "", "", ""
	}
	var out struct {
		DeviceID       string `json:"device_id"`
		DevicePassword string `json:"device_password"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode register response: %v (body: %s)", err, rec.Body.String())
	}
	return rec, out.DeviceID, out.DevicePassword, out.Status
}

// pollDeviceStatus asks the status endpoint for a device's approval state.
func pollDeviceStatus(a *App, email, deviceID, devicePassword string) *httptest.ResponseRecorder {
	return postMemberJSON(a.handleMemberVayuMailDeviceStatus, "/api/v1/members/vayumail-device-status",
		map[string]string{"email": email, "device_id": deviceID, "device_password": devicePassword})
}

// deviceAction drives the admin console's device-action handler (approve /
// block / remove / require-set) the way the HTMX card posts it.
func deviceAction(a *App, vals url.Values, u *users.User) *httptest.ResponseRecorder {
	return postAppPwForm(a.handleVayuOSDeviceAction, "/os/vayumail/devices/action", vals, u)
}

// deviceIDRe pins the device-id format: 32 lowercase hex characters (128 bits).
var deviceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestDeviceRegisterPendingLifecycle walks the full approval lifecycle:
// register → pending (no sync, raw password no sync either) → approve →
// syncs → block → rejected again — with the status endpoint tracking every
// transition.
func TestDeviceRegisterPendingLifecycle(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()
	const email = "dana@example.com"

	rec, deviceID, devicePw, status := registerDevice(t, a, email, "main-mailbox-pass", "Dana's phone", "android")
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store (the response carries a one-time secret)", cc)
	}
	if status != vmail.DeviceStatusPending {
		t.Fatalf("new device status = %q, want pending", status)
	}
	if !deviceIDRe.MatchString(deviceID) {
		t.Fatalf("device_id = %q, want 32 hex characters", deviceID)
	}
	if !appPwSecretRe.MatchString(devicePw) {
		t.Fatalf("device_password = %q, want the dash-grouped app-password format", devicePw)
	}

	// The status endpoint sees the pending state.
	srec := pollDeviceStatus(a, email, deviceID, devicePw)
	if srec.Code != http.StatusOK || !strings.Contains(srec.Body.String(), `"pending"`) {
		t.Fatalf("status poll = %d %s, want 200 pending", srec.Code, srec.Body.String())
	}

	// Mail-protocol auth path: the pending device cannot sync, and neither can
	// the raw mailbox password while approval is required (the default).
	bridge := &vayuMailBridge{app: a}
	if bridge.verifyCredential(ctx, email, devicePw) {
		t.Error("pending device credential authenticated on the mail path")
	}
	if bridge.verifyCredential(ctx, email, "main-mailbox-pass") {
		t.Error("raw mailbox password authenticated on the mail path while approval is required")
	}
	// …but the web-bootstrap scope (member login / device registration) still
	// accepts the raw password, so a holder can always enrol a device.
	if !bridge.verifyCredentialWeb(ctx, email, "main-mailbox-pass") {
		t.Error("raw mailbox password rejected on the member bootstrap path")
	}

	// Approve from the console → the device (and only the device) syncs.
	devices := a.vayuMail.Accounts().ListDevices(ctx)
	if len(devices) != 1 || devices[0].DeviceID != deviceID || devices[0].Platform != "android" || devices[0].Label != "Dana's phone" {
		t.Fatalf("devices = %+v, want the one registered device", devices)
	}
	id := strconv.FormatInt(devices[0].ID, 10)
	arec := deviceAction(a, url.Values{"op": {"approve"}, "email": {email}, "id": {id}}, admin)
	if arec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200 (body: %s)", arec.Code, arec.Body.String())
	}
	if srec = pollDeviceStatus(a, email, deviceID, devicePw); !strings.Contains(srec.Body.String(), `"approved"`) {
		t.Fatalf("status after approve = %s, want approved", srec.Body.String())
	}
	if !bridge.verifyCredential(ctx, email, devicePw) {
		t.Error("approved device credential rejected on the mail path")
	}
	if bridge.verifyCredential(ctx, email, "main-mailbox-pass") {
		t.Error("raw mailbox password must stay rejected on the mail path even with an approved device")
	}
	// Approval bumps last-used telemetry.
	if got := a.vayuMail.Accounts().ListDevices(ctx); len(got) != 1 || got[0].LastUsedAt.IsZero() {
		t.Error("successful device auth should record last_used_at")
	}

	// Block → rejected again, and the status endpoint says so.
	arec = deviceAction(a, url.Values{"op": {"block"}, "email": {email}, "id": {id}}, admin)
	if arec.Code != http.StatusOK {
		t.Fatalf("block status = %d, want 200", arec.Code)
	}
	if bridge.verifyCredential(ctx, email, devicePw) {
		t.Error("blocked device credential authenticated on the mail path")
	}
	if srec = pollDeviceStatus(a, email, deviceID, devicePw); !strings.Contains(srec.Body.String(), `"blocked"`) {
		t.Fatalf("status after block = %s, want blocked", srec.Body.String())
	}

	// Remove → the credential row is gone entirely.
	arec = deviceAction(a, url.Values{"op": {"remove"}, "email": {email}, "id": {id}}, admin)
	if arec.Code != http.StatusOK {
		t.Fatalf("remove status = %d, want 200", arec.Code)
	}
	if got := a.vayuMail.Accounts().ListDevices(ctx); len(got) != 0 {
		t.Fatalf("after remove want 0 devices, got %d", len(got))
	}
	if srec = pollDeviceStatus(a, email, deviceID, devicePw); srec.Code != http.StatusUnauthorized {
		t.Fatalf("status poll after remove = %d, want 401", srec.Code)
	}
}

// TestDeviceApprovalToggleOff pins the opt-out: with require_device_approval
// switched off, registration approves immediately and the raw mailbox
// password syncs mail again.
func TestDeviceApprovalToggleOff(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	ctx := context.Background()
	const email = "dana@example.com"

	rec := deviceAction(a, url.Values{"op": {"require-set"}, "email": {email}, "on": {"0"}}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("require-set status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if a.vayuMail.Accounts().RequireDeviceApproval(ctx, email) {
		t.Fatal("require-set off did not stick")
	}

	bridge := &vayuMailBridge{app: a}
	if !bridge.verifyCredential(ctx, email, "main-mailbox-pass") {
		t.Error("raw mailbox password should sync when approval is not required")
	}
	rrec, _, devicePw, status := registerDevice(t, a, email, "main-mailbox-pass", "Laptop", "linux")
	if rrec.Code != http.StatusOK || status != vmail.DeviceStatusApproved {
		t.Fatalf("register with approval off = %d/%q, want 200/approved", rrec.Code, status)
	}
	if !bridge.verifyCredential(ctx, email, devicePw) {
		t.Error("auto-approved device credential rejected on the mail path")
	}

	// Flip it back on: the password is retired from the mail path again.
	if rec = deviceAction(a, url.Values{"op": {"require-set"}, "email": {email}, "on": {"1"}}, admin); rec.Code != http.StatusOK {
		t.Fatalf("require-set on status = %d, want 200", rec.Code)
	}
	if bridge.verifyCredential(ctx, email, "main-mailbox-pass") {
		t.Error("raw mailbox password authenticated on the mail path after re-enabling approval")
	}
}

// TestDeviceEndpointsNoEnumeration verifies the anti-enumeration posture of
// both member endpoints: an unknown identity and a wrong secret produce
// byte-identical 401s.
func TestDeviceEndpointsNoEnumeration(t *testing.T) {
	a := appWithMailAccounts(t)

	// Register: wrong password vs unknown mailbox.
	wrong, _, _, _ := registerDevice(t, a, "dana@example.com", "not-the-password", "Phone", "android")
	unknown, _, _, _ := registerDevice(t, a, "ghost@example.com", "not-the-password", "Phone", "android")
	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("register statuses = %d / %d, want 401 / 401", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("register wrong-password and unknown-mailbox responses differ (enumeration leak):\n wrong:   %s\n unknown: %s",
			wrong.Body.String(), unknown.Body.String())
	}

	// Status: a real device with a wrong secret vs a device that never existed.
	rec, deviceID, _, _ := registerDevice(t, a, "dana@example.com", "main-mailbox-pass", "Phone", "android")
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200", rec.Code)
	}
	wrongPw := pollDeviceStatus(a, "dana@example.com", deviceID, "AaaaBbbbCcccDdddEeee")
	noDevice := pollDeviceStatus(a, "dana@example.com", strings.Repeat("0", 32), "AaaaBbbbCcccDdddEeee")
	if wrongPw.Code != http.StatusUnauthorized || noDevice.Code != http.StatusUnauthorized {
		t.Fatalf("status statuses = %d / %d, want 401 / 401", wrongPw.Code, noDevice.Code)
	}
	if wrongPw.Body.String() != noDevice.Body.String() {
		t.Errorf("status wrong-secret and unknown-device responses differ (enumeration leak):\n wrong:   %s\n unknown: %s",
			wrongPw.Body.String(), noDevice.Body.String())
	}
	// Neither 401 may reveal an approval state.
	if strings.Contains(wrongPw.Body.String(), "pending") || strings.Contains(wrongPw.Body.String(), "approved") {
		t.Error("401 response leaked a device status")
	}
}

// TestDeviceActionAdminOnly pins the authorisation boundary of the console
// handler: the approval anchor is the admin session, so a mailbox holder can
// never approve their own device.
func TestDeviceActionAdminOnly(t *testing.T) {
	a := appWithMailAccounts(t)
	const email = "dana@example.com"
	if _, _, _, status := registerDevice(t, a, email, "main-mailbox-pass", "Phone", "android"); status != vmail.DeviceStatusPending {
		t.Fatalf("register status = %q, want pending", status)
	}
	devices := a.vayuMail.Accounts().ListDevices(context.Background())
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(devices))
	}
	id := strconv.FormatInt(devices[0].ID, 10)

	// Anonymous and the mailbox holder are both refused.
	if rec := deviceAction(a, url.Values{"op": {"approve"}, "email": {email}, "id": {id}}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous approve status = %d, want 403", rec.Code)
	}
	holder := &users.User{ID: "u2", Email: email, Role: users.RoleAuthor, MailAddress: email}
	if rec := deviceAction(a, url.Values{"op": {"approve"}, "email": {email}, "id": {id}}, holder); rec.Code != http.StatusForbidden {
		t.Fatalf("holder approve status = %d, want 403", rec.Code)
	}
	if got := a.vayuMail.Accounts().ListDevices(context.Background()); got[0].Status != vmail.DeviceStatusPending {
		t.Fatalf("device status = %q, want still pending", got[0].Status)
	}
}

// TestVayuMailPrivKeyDeviceApproval verifies the private-key endpoint follows
// the mail-sync scope: with device approval required, the raw mailbox
// password and a pending device are refused, and only an approved device
// credential unlocks the key.
func TestVayuMailPrivKeyDeviceApproval(t *testing.T) {
	a := appWithMailAndPGP(t)
	const email = "dana@example.com"

	// Raw password: refused (approval is required by default).
	if rec := postPrivKey(a, email, "main-mailbox-pass"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("raw-password privkey status = %d, want 401", rec.Code)
	}

	// Pending device: refused too.
	rec, _, devicePw, _ := registerDevice(t, a, email, "main-mailbox-pass", "Phone", "android")
	if rec.Code != http.StatusOK {
		t.Fatalf("register status = %d, want 200", rec.Code)
	}
	if prec := postPrivKey(a, email, devicePw); prec.Code != http.StatusUnauthorized {
		t.Fatalf("pending-device privkey status = %d, want 401", prec.Code)
	}

	// Approved device: the key is served.
	devices := a.vayuMail.Accounts().ListDevices(context.Background())
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(devices))
	}
	if err := a.vayuMail.Accounts().SetDeviceStatus(context.Background(), email, devices[0].ID, vmail.DeviceStatusApproved); err != nil {
		t.Fatalf("approve: %v", err)
	}
	prec := postPrivKey(a, email, devicePw)
	if prec.Code != http.StatusOK {
		t.Fatalf("approved-device privkey status = %d, want 200 (body: %s)", prec.Code, prec.Body.String())
	}
	if !strings.Contains(prec.Body.String(), privKeyHeader) {
		t.Error("approved-device auth did not return a private key")
	}
}

// TestAccountsPageShowsDevicesCard pins the Accounts-page wiring: the Devices
// card is present with its HTMX action target, and a pending device renders
// the prominent pending chip.
func TestAccountsPageShowsDevicesCard(t *testing.T) {
	a := appWithMailAccounts(t)
	admin := &users.User{ID: "admin1", Email: "boss@example.com", Role: users.RoleAdmin}
	if _, _, _, status := registerDevice(t, a, "dana@example.com", "main-mailbox-pass", "Dana's phone", "android"); status != vmail.DeviceStatusPending {
		t.Fatalf("register status = %q, want pending", status)
	}

	req := withUser(httptest.NewRequest(http.MethodGet, "/os/vayumail/accounts", nil), admin)
	rec := httptest.NewRecorder()
	a.handleVayuOSAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accounts status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`id="vm-device-card"`,
		"/os/vayumail/devices/action",
		`badge--pending`,
		"Dana&#39;s phone",
		"Require device approval",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Accounts page missing %q", want)
		}
	}
}
