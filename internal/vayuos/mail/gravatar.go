package mail

// gravatar.go — Libravatar/Gravatar-compatible avatar lookup by hash, so other
// mail systems and clients that support avatar federation can fetch a mailbox's
// profile picture from a public /avatar/<hash> URL. The hash is md5 (Gravatar/
// Libravatar classic) or sha256 (Libravatar secure) of the lowercased address.

import (
	"context"
	"crypto/md5" //nolint:gosec // md5 is required by the Gravatar/Libravatar spec (identifier hash, not security)
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// errNoAvatarForHash means no active mailbox with a picture matches the hash.
var errNoAvatarForHash = errors.New("vayumail: no avatar for hash")

// EmailAvatarHashes returns the md5 and sha256 hex digests of a normalised email,
// the two identifiers Gravatar (md5) and Libravatar (md5 + sha256) look an avatar
// up by. Exposed so callers can advertise a sender's avatar URL if they wish.
func EmailAvatarHashes(email string) (md5hex, sha256hex string) {
	e := normEmail(email)
	m := md5.Sum([]byte(e)) //nolint:gosec // spec-mandated identifier hash
	s := sha256.Sum256([]byte(e))
	return hex.EncodeToString(m[:]), hex.EncodeToString(s[:])
}

// AvatarByHash returns the stored picture for the mailbox whose address hashes to
// the given md5 or sha256 hex digest (Libravatar/Gravatar federation), or an
// error when no active mailbox with a picture matches. The lookup lists accounts
// and compares digests; mail installs have a handful of mailboxes, so this stays
// cheap and needs no extra index.
func (s *AccountStore) AvatarByHash(ctx context.Context, hash string) ([]byte, string, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if s.db == nil || len(hash) < 32 {
		return nil, "", errNoAvatarForHash
	}
	accs, err := s.List(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, ac := range accs {
		if strings.TrimSpace(ac.AvatarType) == "" {
			continue
		}
		md5hex, sha256hex := EmailAvatarHashes(ac.Email)
		if hash == md5hex || hash == sha256hex {
			return s.Avatar(ctx, ac.Email)
		}
	}
	return nil, "", errNoAvatarForHash
}
