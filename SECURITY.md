# Security Policy

## Supported Versions

| Version       | Security Support         |
|---------------|--------------------------|
| 1.0.x (latest)| Full — all CVEs          |
| LTS (1.x)     | Critical only (CVSS ≥9.0)|
| < 0.9.0       | None — please upgrade    |

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
