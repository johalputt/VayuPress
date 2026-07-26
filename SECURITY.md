# Security Policy

## Supported Versions

| Version | Security support |
|---|---|
| **3.15.x** (current) | **Full.** Every severity; fixes ship in the next micro release |
| **3.14.x** (previous minor) | **Critical only** (CVSS ≥ 9.0), until **31 December 2026** |
| < 3.14 | **None.** Upgrade |

Releases are frequent and micro-version upgrades are drop-in — the supported
window is deliberately narrow because staying current is cheap here, not because
older versions are abandoned casually.

**Check your version** with `vayupress version`, or in VayuOS under
**Settings → About**.

### What this project cannot promise, said plainly

The EU Cyber Resilience Act sets a default expectation of a **five-year** support
period for products with digital elements. **VayuPress does not offer that, and a
single-maintainer project funded by donations should not claim to.** Saying
otherwise would be the kind of promise that gets discovered to be worthless at
exactly the wrong moment.

What exists instead is the thing that actually protects an operator who needs a
longer horizon, and it does not depend on anyone's continued goodwill:

- Every release is **tagged and cosign-signed**, so any version can be verified
  and rebuilt from source.
- The whole codebase is **Apache-2.0** and that grant is irrevocable, so anyone
  needing support beyond the window above can maintain their own branch — with
  no permission required, no licence to renew, and no vendor to negotiate with.
- A **CycloneDX SBOM** is attached to every GitHub Release, so the dependency
  inventory of any deployed version stays answerable after the fact.

If you are deploying VayuPress somewhere that requires a contractual support
term, that requirement is real and this project does not meet it. Budget for
maintaining a fork, or choose something with a vendor behind it.

Scope position under the CRA, and the gap analysis behind these statements, is in
[docs/CRA-READINESS.md](docs/CRA-READINESS.md).

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Email: **security@vayupress.com** (PGP optional)

We follow responsible disclosure:

| Severity              | SLA                          |
|-----------------------|------------------------------|
| Critical (CVSS ≥ 9.0) | Patch within 72 hours        |
| High (CVSS ≥ 7.0)     | Patch within 1 week          |
| Medium / Low          | Next MINOR release           |

**Process**:
1. Email security@vayupress.com with reproduction steps and impact assessment.
2. We acknowledge within 24 hours.
3. We triage, assign CVSS, and notify you of timeline.
4. We patch on a private branch and prepare a CVE request if applicable.
5. We release the fix and publish a security advisory.
6. No public details are disclosed before the patch ships.

## Security Principles

- Defense in Depth
- Least Privilege
- Fail Securely (deny by default)
- Transparency (no security through obscurity)
- Zero Trust
- Privacy by Design

## Scope

In-scope: VayuPress core binary, deploy scripts, Go source, SQLite schema, Nginx config templates.

Out-of-scope: Third-party themes/plugins not maintained by the core team, user-operated infrastructure.

## Bug Bounty

A bug bounty program is planned. Updates will be published at https://vayupress.com/security.

## Security Headers

VayuPress enforces by default:
- `Strict-Transport-Security`
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- Strict Content Security Policy with per-request nonces
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Permissions-Policy`

## Contact

security@vayupress.com — abuse@vayupress.com — privacy@vayupress.com
