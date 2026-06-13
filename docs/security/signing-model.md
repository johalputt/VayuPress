# Signing Model — VayuPress

**Status:** Authoritative  
**Last reviewed:** 2026-06-13

---

## What Is Signed

| Artifact | Algorithm | When Signed | Verification Point |
|----------|-----------|-------------|-------------------|
| Published articles | Ed25519 | On publish via `signing.Sign()` | Before serving via `signing.Verify()` |
| Plugin binaries | SHA-256 digest | At plugin registration | Before `cmd.Start()` in `start()` |
| Migration files | SHA-256 digest | At migration authoring | At migration engine load time |
| Immutable archive objects | SHA-256 content address | At archive write | At archive read |
| Merkle tree roots | SHA-256 tree | On snapshot | At snapshot verification |

---

## Article Signing

```go
// ArticlePayload fields included in signature:
type ArticlePayload struct {
    ID          string    // stable identifier
    Title       string
    Body        string
    AuthorDID   string    // DID:key of author
    PublishedAt time.Time
    Version     uint32    // monotonic; prevents rollback
}
// Signed as: ed25519.Sign(priv, sha256(canonicalJSON(payload)))
```

**Canonical JSON:** keys sorted alphabetically, no whitespace, UTF-8 encoded.  
**Public key:** hex-encoded in `SignedArticle.PublicKeyHex`.  
**Signature:** hex-encoded in `SignedArticle.SignatureHex`.

Verification is **always** called before article content is served. A verification failure triggers:
1. HTTP 500 to the requesting client.
2. Structured error log at `level=error, component=signing`.
3. Incident counter increment in metrics.

---

## Plugin Binary Verification

```go
// Manifest.ExecutableHash: "sha256:<hex>"
// Checked in SubprocessPlugin.start() before exec.Command is created.
```

If the binary hash does not match:
- `start()` returns an error immediately.
- The plugin is NOT started.
- The mismatch is logged at `level=error`.
- No restart is attempted (this is not a crash; it is a security violation).

---

## Migration Checksum Integrity

Each `.sql` migration file has its SHA-256 stored in `schema_migrations` on first run.  
On subsequent runs, the stored checksum is compared. A mismatch means the migration file  
was modified after deployment — treated as a fatal error (ADR-0034).

---

## Key Rotation Procedure

1. Generate new Ed25519 keypair: `vayupress keygen --output /etc/vayupress/signing.key.new`
2. Publish new public key to `/.well-known/vayupress-signing-key`
3. Re-sign all existing articles: `vayupress resign --key /etc/vayupress/signing.key.new`
4. Atomically replace: `mv /etc/vayupress/signing.key.new /etc/vayupress/signing.key`
5. Reload service: `systemctl reload vayupress`
6. Verify: `vayupress verify-all-articles`

Old signatures become invalid after rotation. There is intentionally no "multi-key" grace period —  
any article with an old signature must be re-signed before it can be served.

---

## Merkle Integrity

Snapshots use a SHA-256 binary Merkle tree over article content hashes.  
The root hash is stored alongside the snapshot and can be independently verified:

```
vayupress snapshot verify --root <hex-root> --snapshot <path>
```

The `internal/merkle` package implements RFC-6962-compatible proof generation and verification.
