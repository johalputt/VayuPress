// SPDX-License-Identifier: Apache-2.0

package members

import (
	"context"
	"fmt"
	"strings"
)

// MaxAvatarBytes caps an uploaded member photo at 100 KB (product decision):
// small enough to store inline and serve fast, large enough for a crisp face.
const MaxAvatarBytes = 100 * 1024

// AvatarPhoto is the avatar_choice sentinel meaning "show my uploaded photo".
// An empty choice means the deterministic, gender-aware auto avatar; "cartoon:N"
// means a prebuilt cartoon.
const AvatarPhoto = "photo"

// SetGender sets the member's optional gender ("male", "female", "neutral" or
// "" to unset). It only steers the auto avatar.
func (s *Store) SetGender(ctx context.Context, email, gender string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE members SET gender=? WHERE email=?`,
		strings.TrimSpace(gender), strings.ToLower(strings.TrimSpace(email)))
	return err
}

// SetAvatarChoice records which avatar the member shows ("" auto, "photo",
// "cartoon:N"). It never touches the stored photo blob, so a member can switch to
// a cartoon and back to their photo without re-uploading.
func (s *Store) SetAvatarChoice(ctx context.Context, email, choice string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE members SET avatar_choice=? WHERE email=?`,
		strings.TrimSpace(choice), strings.ToLower(strings.TrimSpace(email)))
	return err
}

// SetAvatarPhoto stores an uploaded photo (the caller has already validated its
// size and image type) and switches the member's avatar to it.
func (s *Store) SetAvatarPhoto(ctx context.Context, email string, blob []byte, mime string) error {
	if len(blob) == 0 || mime == "" {
		return fmt.Errorf("empty avatar")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE members SET avatar_blob=?, avatar_mime=?, avatar_choice=? WHERE email=?`,
		blob, mime, AvatarPhoto, strings.ToLower(strings.TrimSpace(email)))
	return err
}

// AvatarServeInfo is everything the public avatar endpoint needs to render a
// member's avatar from a single id lookup: the email (the deterministic seed for
// generated avatars), the choice, the gender, and — only when Choice == "photo" —
// the stored blob + mime.
type AvatarServeInfo struct {
	Email  string
	Choice string
	Gender string
	Blob   []byte
	Mime   string
}

// AvatarByID loads the avatar-serve info for a member id (primary-key lookup).
func (s *Store) AvatarByID(ctx context.Context, id string) (*AvatarServeInfo, error) {
	var info AvatarServeInfo
	row := s.db.QueryRowContext(ctx,
		`SELECT email, COALESCE(gender,''), COALESCE(avatar_choice,''), avatar_blob, COALESCE(avatar_mime,'') FROM members WHERE id=?`, id)
	if err := row.Scan(&info.Email, &info.Gender, &info.Choice, &info.Blob, &info.Mime); err != nil {
		return nil, err
	}
	return &info, nil
}
