// SPDX-License-Identifier: Apache-2.0

package mail

// calendar_caldav.go — a subscribed calendar read from another CalDAV
// server, signed in with the holder's name and an app password there: for a
// calendar that is not published at an address of its own. It is read the
// way an address is (calendar_subs.go), every half hour, into the same kept
// copy, and shown read only; nothing is written to the other server.
//
// The password is kept sealed by the install's secrets store (AES-256-GCM
// under its data key; the codec that seals mailbox TOTP seeds), never in
// the clear: with no store to seal it, a subscription that needs one is
// refused, and a value that is not sealed is never opened or sent. The other server is reached
// through safefetch's guarded transport, so the server's own network is
// refused for it as for an address, and every answer is bounded.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/johalputt/vayupress/internal/safefetch"
)

// sealedSecret marks a password sealed by the secrets store (SealField's
// own prefix); anything else is not opened, so a password is never read
// back from the clear.
const sealedSecret = "f1."

var (
	ErrCalDAVNoSealer = errors.New("vayumail: this install has no secrets store to keep a password in, so a calendar that needs one cannot be added")
	errCalDAVTooLarge = errors.New("the calendar is larger than 4 MB")
)

// AddCalDAVSubscription subscribes rd's calendar to the calendar at rawURL
// on another CalDAV server, signed in as user with password, and reads it
// once.
func (e *Engine) AddCalDAVSubscription(rd Reader, name, rawURL, user, password string, busy bool, now time.Time) (int64, error) {
	if e.totpCodec == nil {
		return 0, ErrCalDAVNoSealer
	}
	addr, err := calSubURL(rawURL)
	if err != nil {
		return 0, err
	}
	if !strings.HasPrefix(addr, "https://") {
		// A password goes to another server only over TLS.
		return 0, errors.New("vayumail: a calendar signed in to is reached over https")
	}
	user = strings.TrimSpace(user)
	if user == "" || password == "" || len(user) > 200 || len(password) > 500 {
		return 0, errors.New("vayumail: a calendar on another server needs its user name and password there")
	}
	sealed, err := e.totpCodec.SealField(password)
	if err != nil || !strings.HasPrefix(sealed, sealedSecret) {
		return 0, ErrCalDAVNoSealer
	}
	return e.addSubscription(rd, name, addr, user, sealed, busy, now)
}

// fetchCalDAV reads every event and task of the calendar at addr into one
// iCalendar file.
func (e *Engine) fetchCalDAV(ctx context.Context, addr, user, sealed string) ([]byte, error) {
	if e.totpCodec == nil || !strings.HasPrefix(sealed, sealedSecret) {
		return nil, ErrCalDAVNoSealer
	}
	password, err := e.totpCodec.OpenField(sealed)
	if err != nil {
		return nil, errors.New("the password could not be opened")
	}
	rt := e.calDAVTransport
	if rt == nil {
		rt = safefetch.SafeTransport(safefetch.TransportOptions{DialTimeout: 15 * time.Second})
	}
	hc := &http.Client{Transport: boundedBodies{rt}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		// A CalDAV server that moves the calendar is asked again by the
		// holder, at its new address; a password is not followed.
		return http.ErrUseLastResponse
	}}
	c, err := caldav.NewClient(webdav.HTTPClientWithBasicAuth(hc, user, password), addr)
	if err != nil {
		return nil, err
	}
	objs, err := c.QueryCalendar(ctx, "", &caldav.CalendarQuery{
		CompRequest: caldav.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
		CompFilter:  caldav.CompFilter{Name: ical.CompCalendar},
	})
	if err != nil {
		if errors.Is(err, errCalDAVTooLarge) || errors.Is(err, safefetch.ErrBlockedAddress) {
			return nil, err
		}
		msg := err.Error()
		if strings.Contains(msg, "401") || strings.Contains(msg, "403") {
			return nil, errors.New("the server refused the user name and password")
		}
		return nil, errors.New("the server did not answer as a CalDAV calendar: give the calendar's own address")
	}
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropProductID, calProdID)
	out.Props.SetText(ical.PropVersion, "2.0")
	zones := map[string]bool{}
	for _, o := range objs {
		if o.Data == nil {
			continue
		}
		for _, ch := range o.Data.Children {
			if ch.Name == ical.CompTimezone {
				id, _ := ch.Props.Text(ical.PropTimezoneID)
				if zones[id] {
					continue
				}
				zones[id] = true
			}
			out.Children = append(out.Children, ch)
		}
	}
	if len(out.Children) == 0 {
		return nil, errors.New("the calendar has no events")
	}
	var b bytes.Buffer
	if err := ical.NewEncoder(&b).Encode(out); err != nil {
		return nil, errors.New("the calendar could not be read")
	}
	if b.Len() > maxCalSubBytes {
		return nil, errCalDAVTooLarge
	}
	return b.Bytes(), nil
}

// boundedBodies reads at most maxCalSubBytes of any answer; past it the read
// fails rather than holding a server's whole reply in memory.
type boundedBodies struct{ rt http.RoundTripper }

func (b boundedBodies) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := b.rt.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedBody{rc: resp.Body, left: maxCalSubBytes}
	return resp, nil
}

type boundedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errCalDAVTooLarge
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *boundedBody) Close() error { return b.rc.Close() }
