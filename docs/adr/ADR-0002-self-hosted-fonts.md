# ADR-0002: Self-Hosted Fonts (Zero Telemetry Doctrine)

**Status**: Accepted  
**Date**: 2024-01-01  
**Author**: @johalputt

## Context

VayuPress uses Inter and IBM Plex Mono as its typefaces. These are commonly loaded from Google Fonts or similar CDNs.

## Decision

All fonts are downloaded at deploy time from jsDelivr (npm CDN) and served from `/static/fonts/` on the VayuPress instance. No external font CDN is contacted at runtime.

## Rationale

- External font CDNs leak user IP addresses to third parties.
- GDPR and privacy regulations in many jurisdictions require explicit consent for such tracking.
- VayuPress's Privacy by Design principle prohibits telemetry by default.
- jsDelivr is used only at deploy time (by the admin running the script), not by end users.

## Alternatives Considered

- **Google Fonts at runtime**: Rejected — sends user IPs to Google on every page load.
- **System fonts only**: Acceptable fallback if download fails; Inter provides better cross-platform consistency.

## Consequences

- Positive: Zero font-load telemetry for users.
- Negative: Deploy script must download fonts; fails gracefully with system-ui fallback.

## Ethical Implications

Fully aligned with Privacy by Design. Removes a common GDPR compliance issue from the default configuration.
