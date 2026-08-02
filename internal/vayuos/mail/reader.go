// SPDX-License-Identifier: Apache-2.0

package mail

// reader.go — who is opening this mailbox, and by what authority (ADR-0152).
//
// The question "may this caller read this mailbox?" used to be answered in the
// cmd layer, separately, at every entry point: a helper in one place and a bare
// `if !a.isAdminRequest(r)` in a dozen others, with at least one handler that
// asked nobody at all. That shape has a specific failure mode — you fix one call
// site, miss another, and the suite stays green while a working operator read
// path remains. It is also the shape that makes "after handover we can no longer
// open your mailbox" impossible to state truthfully, because nobody can enumerate
// the ways in.
//
// So the authority becomes a REQUIRED ARGUMENT to every read. A caller that does
// not supply one does not compile, and the zero value is not a valid answer: an
// unset Reader is refused rather than treated as anything. Adding a new read
// entry point next year cannot silently inherit permission, because there is no
// permission to inherit — it has to be passed.

import "errors"

// ErrNoReadAuthority is returned when a read is attempted with an unset Reader.
// It means a caller reached the engine without saying who was asking, which is a
// programming error rather than a permission decision.
var ErrNoReadAuthority = errors.New("vayumail: read attempted without authority")

// readerKind is the authority a read is made under. The zero value is not a
// valid answer — see the package comment above.
type readerKind uint8

const (
	readerUnset    readerKind = iota // zero value — never valid
	readerOwner                      // the mailbox's own holder, authenticated as themselves
	readerOperator                   // an install administrator acting on someone's mailbox
	readerSystem                     // the product itself (delivery, indexing, quota)
)

// Reader carries the authority for one mailbox read.
//
// It is deliberately opaque and constructed only through the three functions
// below, so a caller cannot assemble one field by field and end up with an
// authority nobody granted.
type Reader struct {
	kind  readerKind
	key   string // the mailbox key being read (local part, or full address)
	actor string // who, or why, for the access record
}

// ReadAsOwner is the mailbox's own holder reading their own mail.
func ReadAsOwner(key string) Reader {
	return Reader{kind: readerOwner, key: key}
}

// ReadAsOperator is an install administrator reading someone else's mailbox.
// actor identifies them, because this is the authority that has to be recorded.
func ReadAsOperator(key, actor string) Reader {
	return Reader{kind: readerOperator, key: key, actor: actor}
}

// ReadAsSystem is the product reading a mailbox on nobody's behalf — delivery,
// quota measurement, an index rebuild. reason names the job so a read that turns
// out to be unexpected can be traced to the code that made it.
func ReadAsSystem(key, reason string) Reader {
	return Reader{kind: readerSystem, key: key, actor: reason}
}

// Key returns the mailbox this Reader is authorised against.
func (r Reader) Key() string { return r.key }

// Actor returns who made the read, or why, for the access record.
func (r Reader) Actor() string { return r.actor }

// IsOperator reports whether this read is an administrator acting on a mailbox
// that is not theirs. This is the only kind a handover can refuse.
func (r Reader) IsOperator() bool { return r.kind == readerOperator }

// valid reports whether an authority was actually supplied.
func (r Reader) valid() bool {
	return r.kind == readerOwner || r.kind == readerOperator || r.kind == readerSystem
}

// readAuthorised is the single gate every engine read passes through.
//
// It currently enforces only that an authority was supplied. The handover
// refusal attaches HERE and nowhere else, which is the whole reason the
// consolidation came first: one function to change, rather than a dozen call
// sites to find and a suite that stays green when one is missed.
func (e *Engine) readAuthorised(rd Reader) error {
	if !rd.valid() {
		return ErrNoReadAuthority
	}
	return nil
}
