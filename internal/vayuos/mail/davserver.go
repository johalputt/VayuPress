// SPDX-License-Identifier: Apache-2.0

package mail

// davserver.go — CardDAV and CalDAV for a mailbox's own apps, over what
// dav.go keeps. go-webdav answers the protocols; this file decides who is
// asking, which mailbox an address names, and what may be kept.
//
// Every request is signed in, with the mailbox's address and a password the
// mail protocols accept (the signIn the console passes, which is IMAP's check
// and throttle): HTTP holds no session, so there is nothing to sign in once.
// The mailbox is the one signed in, never one read from the address: each
// backend call checks its path against the signed-in mailbox's, since a
// REPORT names entries by href in its body, past anything the address says.
//
// One address book and one calendar, at fixed addresses. Neither can be
// made or deleted from an app, which keeps the console's contacts the one
// address book:
//
//	/dav/card/<mailbox>/books/contacts/<card>
//	/dav/cal/<mailbox>/calendars/calendar/<event>
//
// /.well-known/carddav and /.well-known/caldav lead an app to the start of
// each (RFC 6764), so the server's name is all a person types.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/emersion/go-webdav/carddav"
)

const (
	davCardRoot = "/dav/card"
	davCalRoot  = "/dav/cal"
	// maxDAVRequest bounds a request body: one entry, or a REPORT naming
	// many, read before anything is decided about it.
	maxDAVRequest = 2 << 20
	// maxDAVName bounds an entry's name. A made card's name spells its
	// address (madeCardName), up to about 420 characters.
	maxDAVName = 512
)

// davMethods are the methods CardDAV and CalDAV add to HTTP, which a router
// must know before it routes them.
var davMethods = []string{"PROPFIND", "PROPPATCH", "REPORT", "MKCOL", "MKCALENDAR", "COPY", "MOVE"}

// DAVMethods lists them for the router.
func DAVMethods() []string { return append([]string(nil), davMethods...) }

type davCtxKey struct{}

// davRequest is who a request is from and the condition on a DELETE, which
// go-webdav does not pass to a backend.
type davRequest struct {
	mailbox string
	cond    davCondition
}

func davFrom(ctx context.Context) davRequest {
	rq, _ := ctx.Value(davCtxKey{}).(davRequest)
	return rq
}

// DAVHandler answers CardDAV and CalDAV. signIn checks an address and
// password and gives the mailbox they open, as a full address in lower case.
func (e *Engine) DAVHandler(signIn func(user, password string) (string, bool)) http.Handler {
	books := &carddav.Handler{Backend: davBooks{e.accounts}, Prefix: davCardRoot}
	cals := &caldav.Handler{Backend: davCals{e.accounts}, Prefix: davCalRoot}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/carddav":
			http.Redirect(w, r, davCardRoot+"/", http.StatusMovedPermanently)
			return
		case "/.well-known/caldav":
			http.Redirect(w, r, davCalRoot+"/", http.StatusMovedPermanently)
			return
		}
		var h http.Handler
		switch {
		case r.URL.Path == davCardRoot || strings.HasPrefix(r.URL.Path, davCardRoot+"/"):
			h = books
		case r.URL.Path == davCalRoot || strings.HasPrefix(r.URL.Path, davCalRoot+"/"):
			h = cals
		default:
			http.NotFound(w, r)
			return
		}
		user, password, ok := r.BasicAuth()
		mailbox := ""
		if ok && e.accounts != nil {
			mailbox, ok = signIn(user, password)
		}
		if !ok || mailbox == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="VayuMail", charset="UTF-8"`)
			http.Error(w, "Sign in with the mailbox's address and an app password.", http.StatusUnauthorized)
			return
		}
		if r.Method == "MKCOL" || r.Method == "MKCALENDAR" {
			http.Error(w, errDAVFixed.Error(), http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxDAVRequest)
		rq := davRequest{mailbox: mailbox, cond: davConditionOf(webdav.ConditionalMatch(r.Header.Get("If-Match")), webdav.ConditionalMatch(r.Header.Get("If-None-Match")))}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), davCtxKey{}, rq)))
	})
}

// davConditionOf reads If-Match and If-None-Match. A tag that does not parse
// matches nothing, so a malformed If-Match refuses the write rather than
// letting it through unconditioned.
func davConditionOf(ifMatch, ifNoneMatch webdav.ConditionalMatch) davCondition {
	one := func(m webdav.ConditionalMatch) string {
		if !m.IsSet() || m.IsWildcard() {
			return string(m)
		}
		if t, err := m.ETag(); err == nil {
			return t
		}
		return "\x00"
	}
	return davCondition{ifMatch: one(ifMatch), ifNoneMatch: one(ifNoneMatch)}
}

// davEntry is the entry name a path addresses in the collection at dir
// (with its trailing slash) of the signed-in mailbox.
func davEntry(p, dir string) (string, error) {
	d, name := path.Split(p)
	if d != dir || name == "" || len(name) > maxDAVName || strings.HasPrefix(name, ".") {
		return "", webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._~@+=-", r)) {
			return "", webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
		}
	}
	return name, nil
}

// davErr is err as the status an app reads. A store failure is not spelled
// out to the app.
func davErr(err error, preconditionUID, preconditionSize error) error {
	switch {
	case errors.Is(err, errDAVNotFound):
		return webdav.NewHTTPError(http.StatusNotFound, err)
	case errors.Is(err, errDAVChanged):
		return webdav.NewHTTPError(http.StatusPreconditionFailed, err)
	case errors.Is(err, errDAVUIDTaken):
		return preconditionUID
	case errors.Is(err, errDAVTooLarge):
		return preconditionSize
	case errors.Is(err, errDAVFull):
		return webdav.NewHTTPError(http.StatusInsufficientStorage, err)
	}
	return webdav.NewHTTPError(http.StatusInternalServerError, errors.New("it could not be kept"))
}

var errDAVFixed = webdav.NewHTTPError(http.StatusForbidden, errors.New("there is one address book and one calendar, and they cannot be made or deleted"))

// ── Contacts ─────────────────────────────────────────────────────────────────

type davBooks struct{ s *AccountStore }

func bookHome(ctx context.Context) string {
	return davCardRoot + "/" + davFrom(ctx).mailbox + "/books/"
}

func bookPath(ctx context.Context) string { return bookHome(ctx) + "contacts/" }

func (davBooks) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return davCardRoot + "/" + davFrom(ctx).mailbox + "/", nil
}

func (davBooks) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	return bookHome(ctx), nil
}

func (davBooks) book(ctx context.Context) carddav.AddressBook {
	return carddav.AddressBook{Path: bookPath(ctx), Name: "Contacts", Description: "VayuMail contacts", MaxResourceSize: maxDAVObject}
}

func (b davBooks) ListAddressBooks(ctx context.Context) ([]carddav.AddressBook, error) {
	return []carddav.AddressBook{b.book(ctx)}, nil
}

func (b davBooks) GetAddressBook(ctx context.Context, p string) (*carddav.AddressBook, error) {
	if path.Clean(p)+"/" != bookPath(ctx) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
	}
	ab := b.book(ctx)
	return &ab, nil
}

func (davBooks) CreateAddressBook(context.Context, *carddav.AddressBook) error { return errDAVFixed }

func (davBooks) DeleteAddressBook(context.Context, string) error { return errDAVFixed }

func bookObject(p string, o davObject) (carddav.AddressObject, error) {
	c, err := vcard.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return carddav.AddressObject{}, err
	}
	return carddav.AddressObject{Path: p, ModTime: o.Modified, ETag: o.ETag, Card: c}, nil
}

func (b davBooks) GetAddressObject(ctx context.Context, p string, _ *carddav.AddressDataRequest) (*carddav.AddressObject, error) {
	name, err := davEntry(p, bookPath(ctx))
	if err != nil {
		return nil, err
	}
	o, err := b.s.davGet(ctx, davFrom(ctx).mailbox, davCard, name)
	if err != nil {
		return nil, davErr(err, nil, nil)
	}
	ao, err := bookObject(p, o)
	return &ao, err
}

func (b davBooks) ListAddressObjects(ctx context.Context, p string, _ *carddav.AddressDataRequest) ([]carddav.AddressObject, error) {
	if path.Clean(p)+"/" != bookPath(ctx) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
	}
	list, err := b.s.davList(ctx, davFrom(ctx).mailbox, davCard)
	if err != nil {
		return nil, davErr(err, nil, nil)
	}
	out := make([]carddav.AddressObject, 0, len(list))
	for _, o := range list {
		ao, err := bookObject(bookPath(ctx)+o.Name, o)
		if err != nil {
			return nil, err
		}
		out = append(out, ao)
	}
	return out, nil
}

func (b davBooks) QueryAddressObjects(ctx context.Context, p string, q *carddav.AddressBookQuery) ([]carddav.AddressObject, error) {
	all, err := b.ListAddressObjects(ctx, p, &q.DataRequest)
	if err != nil {
		return nil, err
	}
	// A query with no filter asks for every card (RFC 6352 §10.5); go-webdav
	// reads "any of no filters" as none.
	if len(q.PropFilters) == 0 {
		if q.Limit > 0 && q.Limit < len(all) {
			all = all[:q.Limit]
		}
		return all, nil
	}
	return carddav.Filter(q, all)
}

func (b davBooks) PutAddressObject(ctx context.Context, p string, card vcard.Card, opts *carddav.PutAddressObjectOptions) (*carddav.AddressObject, error) {
	name, err := davEntry(p, bookPath(ctx))
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	if err := vcard.NewEncoder(&data).Encode(card); err != nil {
		return nil, carddav.NewPreconditionError(carddav.PreconditionValidAddressData)
	}
	// RFC 6352 requires a UID; a card without one is kept under its name.
	uid := strings.TrimSpace(card.Value(vcard.FieldUID))
	if uid == "" {
		uid = name
	}
	o, err := b.s.davPut(ctx, davFrom(ctx).mailbox, davCard, name, uid, data.Bytes(), davConditionOf(opts.IfMatch, opts.IfNoneMatch))
	if err != nil {
		return nil, davErr(err, carddav.NewPreconditionError(carddav.PreconditionNoUIDConflict), carddav.NewPreconditionError(carddav.PreconditionMaxResourceSize))
	}
	return &carddav.AddressObject{Path: p, ModTime: o.Modified, ETag: o.ETag, Card: card}, nil
}

func (b davBooks) DeleteAddressObject(ctx context.Context, p string) error {
	name, err := davEntry(p, bookPath(ctx))
	if err != nil {
		return err
	}
	rq := davFrom(ctx)
	if err := b.s.davDelete(ctx, rq.mailbox, davCard, name, rq.cond); err != nil {
		return davErr(err, nil, nil)
	}
	return nil
}

// ── Calendar ─────────────────────────────────────────────────────────────────

type davCals struct{ s *AccountStore }

func calHome(ctx context.Context) string {
	return davCalRoot + "/" + davFrom(ctx).mailbox + "/calendars/"
}

func calPath(ctx context.Context) string { return calHome(ctx) + "calendar/" }

// davComponents are what the calendar keeps: events, and the tasks phones
// keep beside them.
var davComponents = []string{ical.CompEvent, ical.CompToDo}

func (davCals) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return davCalRoot + "/" + davFrom(ctx).mailbox + "/", nil
}

func (davCals) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return calHome(ctx), nil
}

func (davCals) calendar(ctx context.Context) caldav.Calendar {
	return caldav.Calendar{Path: calPath(ctx), Name: "Calendar", Description: "VayuMail calendar", MaxResourceSize: maxDAVObject, SupportedComponentSet: davComponents}
}

func (c davCals) ListCalendars(ctx context.Context) ([]caldav.Calendar, error) {
	return []caldav.Calendar{c.calendar(ctx)}, nil
}

func (c davCals) GetCalendar(ctx context.Context, p string) (*caldav.Calendar, error) {
	if path.Clean(p)+"/" != calPath(ctx) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
	}
	cal := c.calendar(ctx)
	return &cal, nil
}

func (davCals) CreateCalendar(context.Context, *caldav.Calendar) error { return errDAVFixed }

func calObject(p string, o davObject) (caldav.CalendarObject, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(o.Data)).Decode()
	if err != nil {
		return caldav.CalendarObject{}, err
	}
	return caldav.CalendarObject{Path: p, ModTime: o.Modified, ETag: o.ETag, Data: cal}, nil
}

func (c davCals) GetCalendarObject(ctx context.Context, p string, _ *caldav.CalendarCompRequest) (*caldav.CalendarObject, error) {
	name, err := davEntry(p, calPath(ctx))
	if err != nil {
		return nil, err
	}
	o, err := c.s.davGet(ctx, davFrom(ctx).mailbox, davEvent, name)
	if err != nil {
		return nil, davErr(err, nil, nil)
	}
	co, err := calObject(p, o)
	return &co, err
}

func (c davCals) ListCalendarObjects(ctx context.Context, p string, _ *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error) {
	if path.Clean(p)+"/" != calPath(ctx) {
		return nil, webdav.NewHTTPError(http.StatusNotFound, errDAVNotFound)
	}
	list, err := c.s.davList(ctx, davFrom(ctx).mailbox, davEvent)
	if err != nil {
		return nil, davErr(err, nil, nil)
	}
	out := make([]caldav.CalendarObject, 0, len(list))
	for _, o := range list {
		co, err := calObject(calPath(ctx)+o.Name, o)
		if err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, nil
}

func (c davCals) QueryCalendarObjects(ctx context.Context, p string, q *caldav.CalendarQuery) ([]caldav.CalendarObject, error) {
	all, err := c.ListCalendarObjects(ctx, p, &q.CompRequest)
	if err != nil {
		return nil, err
	}
	return caldav.Filter(q, all)
}

func (c davCals) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error) {
	name, err := davEntry(p, calPath(ctx))
	if err != nil {
		return nil, err
	}
	kind, uid, err := caldav.ValidateCalendarObject(cal)
	switch {
	case err != nil || uid == "":
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	case kind != ical.CompEvent && kind != ical.CompToDo:
		return nil, caldav.NewPreconditionError(caldav.PreconditionSupportedCalendarComponent)
	}
	var data bytes.Buffer
	if err := ical.NewEncoder(&data).Encode(cal); err != nil {
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarData)
	}
	o, err := c.s.davPut(ctx, davFrom(ctx).mailbox, davEvent, name, uid, data.Bytes(), davConditionOf(opts.IfMatch, opts.IfNoneMatch))
	if err != nil {
		return nil, davErr(err, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict), caldav.NewPreconditionError(caldav.PreconditionMaxResourceSize))
	}
	return &caldav.CalendarObject{Path: p, ModTime: o.Modified, ETag: o.ETag, Data: cal}, nil
}

func (c davCals) DeleteCalendarObject(ctx context.Context, p string) error {
	// go-webdav sends a DELETE of the calendar itself here too.
	if path.Clean(p)+"/" == calPath(ctx) {
		return errDAVFixed
	}
	name, err := davEntry(p, calPath(ctx))
	if err != nil {
		return err
	}
	rq := davFrom(ctx)
	if err := c.s.davDelete(ctx, rq.mailbox, davEvent, name, rq.cond); err != nil {
		return davErr(err, nil, nil)
	}
	return nil
}
