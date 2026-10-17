// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"

	"github.com/johalputt/vayupress/internal/avatar"
	"github.com/johalputt/vayupress/internal/members"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// One person at one address: the holder of a mailbox and the reader account
// of the same address on the site are the same person, with one name and one
// picture. Each was kept in its own store and changed only there, so a name
// changed on the site account went out on mail under the name the mailbox was
// created with (3cs), and a cartoon chosen in one place was the initials in
// the other (3cv). Every change of either now writes both; the stores stay
// two because each serves its own surface (a From header, a comment byline).

// setPersonName gives the person at email the name in both stores. The mail
// store drops control characters and bounds it, as a header value must be;
// the site account is given the same cleaned value. An address with no
// mailbox, or no site account, changes the one it has.
func (a *App) setPersonName(ctx context.Context, email, name string) error {
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if _, ok := a.mailAccountByEmail(ctx, email); ok {
			if err := a.vayuMail.Accounts().SetFullName(ctx, email, name); err != nil {
				return err
			}
			name = a.vayuMail.Accounts().FullNameFor(ctx, email)
		}
	}
	if a.members != nil {
		if m, err := a.members.Get(ctx, email); err == nil && m != nil {
			return a.members.UpdateProfile(ctx, email, name, m.Note)
		}
	}
	return nil
}

// mirrorPictureToMember gives the site account at email the picture its
// mailbox now has: the same cartoon (n >= 0), the uploaded photo when it fits
// the site account's limit, or, with neither, the automatic picture. A photo
// over that limit leaves the site account's picture as it was rather than
// shrinking or refusing a change made in the mail settings.
func (a *App) mirrorPictureToMember(ctx context.Context, email string, n int, blob []byte, mime string) {
	if a.members == nil {
		return
	}
	if m, err := a.members.Get(ctx, email); err != nil || m == nil {
		return
	}
	switch {
	case n >= 0:
		_ = a.members.SetAvatarChoice(ctx, email, cartoonChoice(n))
	case len(blob) > 0:
		if len(blob) <= members.MaxAvatarBytes {
			_ = a.members.SetAvatarPhoto(ctx, email, blob, mime)
		}
	default:
		_ = a.members.SetAvatarChoice(ctx, email, "")
	}
}

// mirrorPictureToMailbox gives the mailbox at email the picture its holder
// chose on the site: the same cartoon, their photo, or (the automatic
// picture) none, which the mailbox draws as initials.
func (a *App) mirrorPictureToMailbox(ctx context.Context, email, choice string, blob []byte, mime string) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return
	}
	if _, ok := a.mailAccountByEmail(ctx, email); !ok {
		return
	}
	acc := a.vayuMail.Accounts()
	if n, ok := cartoonIndex(choice); ok {
		_ = acc.SetAvatar(ctx, email, []byte(avatar.Cartoon(n, email)), "image/svg+xml")
	} else if choice == members.AvatarPhoto && len(blob) > 0 {
		_ = acc.SetAvatar(ctx, email, blob, mime)
	} else {
		_ = acc.ClearAvatar(ctx, email)
	}
	a.invalidateAvatarCache()
}

func cartoonChoice(n int) string { return "cartoon:" + itoaSafe(n) }

// mailRoleLabel is how the console names a mailbox's role, in the words the
// role picker uses. A mail-only session stands in for an author in the
// console's permission checks, and was labelled Author for it (3cu).
func mailRoleLabel(role string) string {
	switch role {
	case vmail.RoleMailbox:
		return "Mailbox: mail only"
	case vmail.RoleReviewer:
		return "Reviewer: read-only mail"
	case "":
		return "Mailbox"
	default:
		return strings.ToUpper(role[:1]) + role[1:]
	}
}
