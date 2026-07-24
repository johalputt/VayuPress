package pgp

import (
	"bytes"
	"context"
	"crypto"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// Engine is the VayuPGP runtime. It owns the encrypted key store, an in-memory
// cache of unlocked entities, and the WKD server.
type Engine struct {
	cfg Config
	ks  *keyStore

	mu       sync.RWMutex
	unlocked map[string]*openpgp.Entity // userID → unlocked private entity

	wkdClient *http.Client
}

// NewEngine constructs a VayuPGP engine. It does not perform I/O; call Start.
func NewEngine(cfg *Config) *Engine {
	if cfg == nil {
		c := DefaultConfig()
		cfg = &c
	}
	return &Engine{
		cfg:      *cfg,
		unlocked: make(map[string]*openpgp.Entity),
		// WKD discovery fetches an attacker-influenced recipient domain, so it must
		// go through the SSRF-hardened dialer (resolve-and-pin, refuse private/
		// reserved IPs, never honour a proxy) — not a default client that would
		// happily reach 127.0.0.1 / 169.254.169.254 via a crafted openpgpkey record
		// or redirect (audit M3). Redirects are refused: WKD responses are direct.
		wkdClient: &http.Client{
			Timeout:   8 * time.Second,
			Transport: safefetch.SafeTransport(safefetch.TransportOptions{}),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Name identifies the subsystem for the boot orchestrator.
func (e *Engine) Name() string { return "VayuPGP" }

// Start opens the key store. It is safe to call when disabled (no-op).
func (e *Engine) Start(_ context.Context) error {
	if !e.cfg.Enabled {
		return nil
	}
	ks, err := newKeyStore(e.cfg.StorageDir, e.cfg.MasterSecret)
	if err != nil {
		return err
	}
	e.ks = ks
	return nil
}

// Stop releases in-memory key material.
func (e *Engine) Stop(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unlocked = make(map[string]*openpgp.Entity)
	return nil
}

func (e *Engine) packetConfig() *packet.Config {
	return &packet.Config{
		Algorithm:       packet.PubKeyAlgoEdDSA,
		Curve:           packet.Curve25519,
		DefaultHash:     crypto.SHA256,
		DefaultCipher:   packet.CipherAES256,
		KeyLifetimeSecs: uint32(e.cfg.KeyExpiry / time.Second),
		SigLifetimeSecs: uint32(e.cfg.KeyExpiry / time.Second),
	}
}

// ── Key lifecycle ────────────────────────────────────────────────────────────

// GenerateKeypair mints an Ed25519 (sign) + Curve25519 (encrypt) keypair for the
// user, stores the private half encrypted at rest, and returns the public view.
func (e *Engine) GenerateKeypair(user *PGPUser) (*Keypair, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	if user == nil || user.Email == "" {
		return nil, errors.New("vayupgp: user email required")
	}
	ent, err := openpgp.NewEntity(user.Name, "VayuPGP", user.Email, e.packetConfig())
	if err != nil {
		return nil, fmt.Errorf("vayupgp: generate entity: %w", err)
	}
	return e.persist(user, ent)
}

// EnsureKeypair returns the existing keypair for the user's email when one is
// already stored, or generates a fresh one otherwise. It is idempotent and
// safe to call repeatedly (e.g. on every account creation or a boot-time
// backfill), so accounts that pre-date auto-keygen still get a key and surface
// in the VayuPGP panel.
func (e *Engine) EnsureKeypair(user *PGPUser) (*Keypair, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	if user == nil || user.Email == "" {
		return nil, errors.New("vayupgp: user email required")
	}
	if userID, ok := e.ks.userIDForEmail(user.Email); ok {
		if rec, err := e.ks.loadMeta(userID); err == nil {
			return recToKeypair(rec), nil
		}
	}
	return e.GenerateKeypair(user)
}

func (e *Engine) persist(user *PGPUser, ent *openpgp.Entity) (*Keypair, error) {
	pubArmor, err := armorEntity(ent, false)
	if err != nil {
		return nil, err
	}
	privArmor, err := armorEntity(ent, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rec := storedKey{
		UserID:      user.UserID,
		Email:       user.Email,
		Name:        user.Name,
		Fingerprint: fingerprintOf(ent),
		PublicArmor: string(pubArmor),
		CreatedAt:   now,
		ExpiresAt:   now.Add(e.cfg.KeyExpiry),
	}
	if err := e.ks.save(rec, privArmor); err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.unlocked[user.UserID] = ent
	e.mu.Unlock()
	return recToKeypair(rec), nil
}

// GetKeypair returns the public view of a stored keypair.
func (e *Engine) GetKeypair(userID string) (*Keypair, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	rec, err := e.ks.loadMeta(userID)
	if err != nil {
		return nil, err
	}
	return recToKeypair(rec), nil
}

// GetPublicKey returns a local recipient's public key (no WKD).
func (e *Engine) GetPublicKey(email string) (*PublicKey, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(email)
	if !ok {
		return nil, ErrNotFound
	}
	rec, err := e.ks.loadMeta(userID)
	if err != nil {
		return nil, err
	}
	return &PublicKey{Email: rec.Email, Fingerprint: rec.Fingerprint, Armor: rec.PublicArmor, Source: "local"}, nil
}

// RevokeKeypair soft-revokes a key (marked revoked, dropped from recipient
// lookup). Old ciphertext remains decryptable.
func (e *Engine) RevokeKeypair(userID string) error {
	if e.ks == nil {
		return errors.New("vayupgp: engine not started")
	}
	rec, priv, err := e.ks.load(userID)
	if err != nil {
		return err
	}
	rec.Revoked = true
	if err := e.ks.save(rec, priv); err != nil {
		return err
	}
	e.ks.mu.Lock()
	delete(e.ks.byEmail, normalizeEmail(rec.Email))
	e.ks.mu.Unlock()
	return nil
}

// RotateKeypair generates a fresh keypair for the user while archiving the
// previous private key so historical ciphertext stays decryptable.
func (e *Engine) RotateKeypair(userID string) (*Keypair, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	rec, priv, err := e.ks.load(userID)
	if err != nil {
		return nil, err
	}
	if err := e.ks.archive(rec, priv); err != nil {
		return nil, err
	}
	user := &PGPUser{UserID: rec.UserID, Name: rec.Name, Email: rec.Email}
	ent, err := openpgp.NewEntity(user.Name, "VayuPGP", user.Email, e.packetConfig())
	if err != nil {
		return nil, err
	}
	return e.persist(user, ent)
}

// ListExpiringKeys returns keys that expire within the given window.
func (e *Engine) ListExpiringKeys(within time.Duration) ([]*Keypair, error) {
	all, err := e.ListKeys()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().UTC().Add(within)
	var out []*Keypair
	for _, k := range all {
		if !k.Revoked && k.ExpiresAt.Before(deadline) {
			out = append(out, k)
		}
	}
	return out, nil
}

// ListKeys returns the public view of every stored keypair.
func (e *Engine) ListKeys() ([]*Keypair, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	recs, err := e.ks.list()
	if err != nil {
		return nil, err
	}
	out := make([]*Keypair, 0, len(recs))
	for _, r := range recs {
		out = append(out, recToKeypair(r))
	}
	return out, nil
}

// GetKeyStatus summarises a key's lifecycle for the panel.
func (e *Engine) GetKeyStatus(userID string) (*KeyStatus, error) {
	kp, err := e.GetKeypair(userID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	st := &KeyStatus{
		UserID: kp.UserID, Email: kp.Email, Fingerprint: kp.Fingerprint,
		CreatedAt: kp.CreatedAt, ExpiresAt: kp.ExpiresAt, Revoked: kp.Revoked,
	}
	st.Expired = now.After(kp.ExpiresAt)
	st.DaysUntilExpiry = int(kp.ExpiresAt.Sub(now).Hours() / 24)
	st.ExpiringSoon = !st.Expired && kp.ExpiresAt.Sub(now) <= e.cfg.RotationNotice
	return st, nil
}

// ── Crypto operations ────────────────────────────────────────────────────────

// entity returns the unlocked private entity for a user, loading + caching it.
func (e *Engine) entity(userID string) (*openpgp.Entity, error) {
	e.mu.RLock()
	ent, ok := e.unlocked[userID]
	e.mu.RUnlock()
	if ok {
		return ent, nil
	}
	_, priv, err := e.ks.load(userID)
	if err != nil {
		return nil, err
	}
	list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(priv))
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("vayupgp: parse private key: %w", err)
	}
	e.mu.Lock()
	e.unlocked[userID] = list[0]
	e.mu.Unlock()
	return list[0], nil
}

// decryptionRing returns the user's current key plus any archived keys so that
// rotation does not break decryption of historical messages.
func (e *Engine) decryptionRing(userID string) (openpgp.EntityList, error) {
	cur, err := e.entity(userID)
	if err != nil {
		return nil, err
	}
	ring := openpgp.EntityList{cur}
	for _, priv := range e.ks.archivedPrivs(userID) {
		if list, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(priv)); err == nil {
			ring = append(ring, list...)
		}
	}
	return ring, nil
}

func entityFromArmor(armored string) (*openpgp.Entity, error) {
	list, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("vayupgp: parse public key: %w", err)
	}
	return list[0], nil
}

// recipientEntity resolves a recipient email to a public entity, trying the
// local key store first and then WKD when AutoEncrypt discovery is desired.
func (e *Engine) recipientEntity(email string) (*openpgp.Entity, error) {
	if pk, err := e.GetPublicKey(email); err == nil {
		return entityFromArmor(pk.Armor)
	}
	pk, err := e.LookupExternalKey(email)
	if err != nil {
		return nil, ErrNotFound
	}
	return entityFromArmor(pk.Armor)
}

// Encrypt produces an armored PGP message for recipientEmail.
func (e *Engine) Encrypt(plaintext []byte, recipientEmail string) ([]byte, error) {
	recip, err := e.recipientEntity(recipientEmail)
	if err != nil {
		return nil, err
	}
	return encryptTo(plaintext, []*openpgp.Entity{recip}, nil)
}

// EncryptToRecipients produces a single armored PGP message encrypted to every
// resolvable recipient in recipientEmails (OpenPGP supports many recipients in
// one message — each gets a session-key packet). It returns the ciphertext plus
// the lowercased list of addresses whose public keys could NOT be found, so a
// caller can decide whether to fall back to plaintext rather than send a message
// some recipient can't read. It errors only when NO recipient could be resolved.
func (e *Engine) EncryptToRecipients(plaintext []byte, recipientEmails []string) ([]byte, []string, error) {
	var entities []*openpgp.Entity
	var missing []string
	seen := make(map[string]bool, len(recipientEmails))
	for _, raw := range recipientEmails {
		em := strings.ToLower(strings.TrimSpace(raw))
		if em == "" || seen[em] {
			continue
		}
		seen[em] = true
		ent, err := e.recipientEntity(em)
		if err != nil || ent == nil {
			missing = append(missing, em)
			continue
		}
		entities = append(entities, ent)
	}
	if len(entities) == 0 {
		return nil, missing, ErrNotFound
	}
	ct, err := encryptTo(plaintext, entities, nil)
	if err != nil {
		return nil, missing, err
	}
	return ct, missing, nil
}

// EncryptAndSign produces an armored PGP message encrypted to recipientEmail and
// signed by senderUserID's key.
func (e *Engine) EncryptAndSign(plaintext []byte, recipientEmail, senderUserID string) ([]byte, error) {
	recip, err := e.recipientEntity(recipientEmail)
	if err != nil {
		return nil, err
	}
	signer, err := e.entity(senderUserID)
	if err != nil {
		return nil, err
	}
	return encryptTo(plaintext, []*openpgp.Entity{recip}, signer)
}

// EncryptAndSignFromEmail produces an armored PGP message encrypted to
// recipientEmail and signed by the local mailbox identified by senderEmail. It
// resolves senderEmail to its internal key userID (the same email→userID index
// DecryptForEmail uses), so a caller can sign as any local mailbox by address
// alone. This is the server-side counterpart to VayuMail Mobile's device-side
// keyring.Encrypt(plaintext, [peer], selfEmail): the VayuTalk web client signs
// on behalf of the authenticated mailbox owner, producing wire-identical
// armored ciphertext so a web sender and an app sender are indistinguishable to
// the relay and to the recipient's signature check.
func (e *Engine) EncryptAndSignFromEmail(plaintext []byte, recipientEmail, senderEmail string) ([]byte, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(senderEmail)
	if !ok {
		return nil, ErrNotFound
	}
	return e.EncryptAndSign(plaintext, recipientEmail, userID)
}

// Decrypt decrypts an armored PGP message addressed to userID.
func (e *Engine) Decrypt(ciphertext []byte, userID string) ([]byte, error) {
	ring, err := e.decryptionRing(userID)
	if err != nil {
		return nil, err
	}
	block, err := armor.Decode(bytes.NewReader(ciphertext))
	if err != nil {
		return nil, fmt.Errorf("vayupgp: dearmor: %w", err)
	}
	md, err := openpgp.ReadMessage(block.Body, ring, nil, e.packetConfig())
	if err != nil {
		return nil, fmt.Errorf("vayupgp: read message: %w", err)
	}
	return io.ReadAll(md.UnverifiedBody)
}

// DecryptForEmail resolves a recipient email to its local key (via the
// email→userID index) and decrypts the message with that account's key ring.
// It lets callers transparently decrypt mail addressed to any local mailbox —
// CMS users and admin-managed mail accounts alike — without first knowing the
// internal userID under which the key is stored.
func (e *Engine) DecryptForEmail(ciphertext []byte, recipientEmail string) ([]byte, error) {
	if e.ks == nil {
		return nil, errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(recipientEmail)
	if !ok {
		return nil, ErrNotFound
	}
	return e.Decrypt(ciphertext, userID)
}

// DecryptAndVerifyForEmail decrypts a message addressed to recipientEmail and, if
// senderEmail's public key is available locally, reports whether the message
// carries a valid signature from that sender. verified is true only when the
// message is signed AND the signature checks out against the sender's key; a
// missing sender key or an unsigned message yields verified=false with the
// plaintext still returned — confidentiality never depends on verification. The
// sender key is looked up locally only (in the Tor world recipientEntity's WKD
// fallback is refused), so no clearnet lookup is triggered here.
func (e *Engine) DecryptAndVerifyForEmail(ciphertext []byte, recipientEmail, senderEmail string) ([]byte, bool, error) {
	if e.ks == nil {
		return nil, false, errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(recipientEmail)
	if !ok {
		return nil, false, ErrNotFound
	}
	ring, err := e.decryptionRing(userID)
	if err != nil {
		return nil, false, err
	}
	// Add the sender's public key to the ring so ReadMessage can identify and
	// check the signature. Best-effort: absence just means we cannot verify.
	if senderEmail != "" {
		if sender, serr := e.recipientEntity(senderEmail); serr == nil && sender != nil {
			ring = append(ring, sender)
		}
	}
	block, err := armor.Decode(bytes.NewReader(ciphertext))
	if err != nil {
		return nil, false, fmt.Errorf("vayupgp: dearmor: %w", err)
	}
	md, err := openpgp.ReadMessage(block.Body, ring, nil, e.packetConfig())
	if err != nil {
		return nil, false, fmt.Errorf("vayupgp: read message: %w", err)
	}
	body, err := io.ReadAll(md.UnverifiedBody)
	if err != nil {
		return nil, false, err
	}
	verified := md.IsSigned && md.SignedBy != nil && md.SignatureError == nil
	return body, verified, nil
}

// Sign returns an armored detached signature over data using userID's key.
func (e *Engine) Sign(data []byte, userID string) ([]byte, error) {
	signer, err := e.entity(userID)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, signer, bytes.NewReader(data), e.packetConfig()); err != nil {
		return nil, fmt.Errorf("vayupgp: sign: %w", err)
	}
	return buf.Bytes(), nil
}

// Verify checks an armored detached signature against senderEmail's public key.
func (e *Engine) Verify(data, sig []byte, senderEmail string) (bool, error) {
	signer, err := e.recipientEntity(senderEmail)
	if err != nil {
		return false, err
	}
	_, err = openpgp.CheckArmoredDetachedSignature(openpgp.EntityList{signer}, bytes.NewReader(data), bytes.NewReader(sig), e.packetConfig())
	return err == nil, err
}

// ── Import / export ──────────────────────────────────────────────────────────

// ExportPublicKey returns the armored public key for userID.
func (e *Engine) ExportPublicKey(userID string) ([]byte, error) {
	kp, err := e.GetKeypair(userID)
	if err != nil {
		return nil, err
	}
	return []byte(kp.PublicArmor), nil
}

// ArmoredPrivateKey returns a mailbox's own PGP private key in armored form
// ("-----BEGIN PGP PRIVATE KEY BLOCK-----…"), resolved by email address. The
// private half is decrypted from its at-rest AES-256-GCM ciphertext — the same
// server-side-decryptable model VayuPGP already uses to read inbound mail — so
// the caller (the mailbox owner's own device, authenticated over TLS) can import
// the key and decrypt received mail on-device. Returns ErrNotFound when no key
// exists for the address; the caller should EnsureKeypair first if it wants one
// generated on demand.
func (e *Engine) ArmoredPrivateKey(email string) (string, error) {
	if e.ks == nil {
		return "", errors.New("vayupgp: engine not started")
	}
	userID, ok := e.ks.userIDForEmail(email)
	if !ok {
		return "", ErrNotFound
	}
	_, priv, err := e.ks.load(userID)
	if err != nil {
		return "", err
	}
	return string(priv), nil
}

// ImportPublicKey parses an armored public key and returns its description.
func (e *Engine) ImportPublicKey(armored []byte) (*PublicKey, error) {
	ent, err := entityFromArmor(string(armored))
	if err != nil {
		return nil, err
	}
	email := ""
	if id := ent.PrimaryIdentity(); id != nil {
		email = id.UserId.Email
	}
	return &PublicKey{Email: email, Fingerprint: fingerprintOf(ent), Armor: string(armored), Source: "import"}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func encryptTo(plaintext []byte, to []*openpgp.Entity, signer *openpgp.Entity) ([]byte, error) {
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP MESSAGE", nil)
	if err != nil {
		return nil, err
	}
	w, err := openpgp.Encrypt(aw, to, signer, nil, &packet.Config{DefaultCipher: packet.CipherAES256, DefaultHash: crypto.SHA256})
	if err != nil {
		_ = aw.Close()
		return nil, fmt.Errorf("vayupgp: encrypt: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func armorEntity(ent *openpgp.Entity, private bool) ([]byte, error) {
	var buf bytes.Buffer
	blockType := openpgp.PublicKeyType
	if private {
		blockType = openpgp.PrivateKeyType
	}
	aw, err := armor.Encode(&buf, blockType, nil)
	if err != nil {
		return nil, err
	}
	if private {
		err = ent.SerializePrivateWithoutSigning(aw, nil)
	} else {
		err = ent.Serialize(aw)
	}
	if err != nil {
		_ = aw.Close()
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fingerprintOf(ent *openpgp.Entity) string {
	return strings.ToUpper(hex.EncodeToString(ent.PrimaryKey.Fingerprint))
}

func recToKeypair(r storedKey) *Keypair {
	return &Keypair{
		UserID: r.UserID, Email: r.Email, Name: r.Name,
		Fingerprint: r.Fingerprint, PublicArmor: r.PublicArmor,
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, Revoked: r.Revoked,
	}
}
