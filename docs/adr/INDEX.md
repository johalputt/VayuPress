# ADR Index — VayuPress

**Auto-maintained:** Update when adding, superseding, or deprecating an ADR.  
**Status states:** `Proposed` | `Accepted` | `Deprecated` | `Superseded` | `Rejected`

---

## Active ADRs

| ADR | Title | Status | Owner | Date |
|-----|-------|--------|-------|------|
| [ADR-0001](ADR-0001-sqlite-first.md) | SQLite-First Data Layer | Accepted | Core | 2024-01-01 |
| [ADR-0093](ADR-0093-indexed-tag-membership.md) | Indexed Tag Membership (article_tags join table) | Accepted | Core | 2026-06-28 |
| [ADR-0092](ADR-0092-block-editor-v2-and-low-impact-deploys.md) | Block Editor v2 — Publishing Options, Content Cards & Low-Impact Deploys | Accepted | VayuOS | 2026-06-27 |
| [ADR-0090](ADR-0090-newsletter-console.md) | Newsletter Console — Operator Page, Subscriber Management & Tracked Broadcasts | Accepted | VayuOS | 2026-06-26 |
| [ADR-0089](ADR-0089-vayuos-one-click-update-and-backup.md) | VayuOS One-Click Update & Full Backup/Restore | Accepted | VayuOS | 2026-06-26 |
| [ADR-0088](ADR-0088-api-keys-and-encrypted-credentials.md) | API Key Console & Encrypted Third-Party Credentials | Accepted | VayuOS | 2026-06-26 |
| [ADR-0076](ADR-0076-vayupgp-at-rest-keys.md) | VayuPGP — Keys Encrypted At Rest | Accepted | VayuOS | 2026-06-24 |
| [ADR-0077](ADR-0077-vayumail-outbound-pure-go.md) | VayuMail Outbound — Pure-Go (No Mox Fork) | Accepted | VayuOS | 2026-06-24 |
| [ADR-0078](ADR-0078-vayumail-inbound-optin.md) | VayuMail Inbound — Opt-In SMTP/IMAP | Accepted | VayuOS | 2026-06-24 |
| [ADR-0079](ADR-0079-vayuanalytics-no-pii.md) | VayuAnalytics — No-PII Rotating Salt | Accepted | VayuOS | 2026-06-24 |
| [ADR-0080](ADR-0080-security-update-watcher.md) | Security-Update Watcher — Opt-In | Accepted | VayuOS | 2026-06-24 |
| [ADR-0002](ADR-0002-self-hosted-fonts.md) | Self-Hosted Fonts | Accepted | Core | 2024-01-01 |
| [ADR-0032](ADR-0032-plugin-pool-waitgroup.md) | Plugin Pool WaitGroup | Accepted | Sandbox | — |
| [ADR-0033](ADR-0033-wal-adaptive-checkpoint.md) | WAL Adaptive Checkpoint | Accepted | DB | — |
| [ADR-0034](ADR-0034-migration-checksum-drift.md) | Migration Checksum Drift | Accepted | Migrations | — |
| [ADR-0035](ADR-0035-dead-letter-queue-safety.md) | Dead-Letter Queue Safety | Accepted | Queue | — |
| [ADR-0036](ADR-0036-csp-nonce.md) | CSP Nonce | Accepted | Security | — |
| [ADR-0037](ADR-0037-pprof-rate-limit.md) | pprof Rate Limit | Accepted | Observability | — |
| [ADR-0038](ADR-0038-vacuum-cooldown.md) | Vacuum Cooldown | Accepted | DB | — |
| [ADR-0039](ADR-0039-deploy-sourced-components.md) | Deploy Sourced Components | Accepted | Deploy | — |
| [ADR-0040](ADR-0040-config-versioning.md) | Config Versioning | Accepted | Config | — |
| [ADR-0041](ADR-0041-health-contracts.md) | Health Contracts | Accepted | Health | — |
| [ADR-0042](ADR-0042-backup-restore-automation.md) | Backup/Restore Automation | Accepted | Operations | — |
| [ADR-0043](ADR-0043-integration-tests.md) | Integration Tests | Accepted | Testing | — |
| [ADR-0044](ADR-0044-repository-decomposition.md) | Repository Decomposition | Accepted | Architecture | — |
| [ADR-0045](ADR-0045-internal-package-decomposition.md) | Internal Package Decomposition | Accepted | Architecture | — |
| [ADR-0046](ADR-0046-runtime-architecture-service-boundaries.md) | Runtime Architecture Service Boundaries | Accepted | Architecture | — |
| [ADR-0047](ADR-0047-app-container-handler-refactor.md) | App Container Handler Refactor | Accepted | Architecture | — |
| [ADR-0048](ADR-0048-route-domains-service-extraction.md) | Route Domains Service Extraction | Accepted | Architecture | — |
| [ADR-0049](ADR-0049-thin-handlers-service-boundaries.md) | Thin Handlers Service Boundaries | Accepted | Architecture | — |
| [ADR-0050](ADR-0050-persistence-transport-maturity.md) | Persistence/Transport Maturity | Accepted | Architecture | — |
| [ADR-0051](ADR-0051-transactional-consistency-event-reliability.md) | Transactional Consistency/Event Reliability | Accepted | Events | — |
| [ADR-0052](ADR-0052-idempotency-event-evolution.md) | Idempotency/Event Evolution | Accepted | Events | — |
| [ADR-0053](ADR-0053-observability-correlation-architecture.md) | Observability Correlation Architecture | Accepted | Observability | — |
| [ADR-0054](ADR-0054-structured-tracing-execution-spans.md) | Structured Tracing/Execution Spans | Accepted | Observability | — |
| [ADR-0055](ADR-0055-resource-governance-execution-isolation.md) | Resource Governance/Execution Isolation | Accepted | Sandbox | — |
| [ADR-0056](ADR-0056-process-isolation-runtime-sandboxing.md) | Process Isolation/Runtime Sandboxing | Accepted | Sandbox | — |
| [ADR-0057](ADR-0057-security-sandboxing-capability-enforcement.md) | Security Sandboxing/Capability Enforcement | Accepted | Security | — |
| [ADR-0058](ADR-0058-kernel-level-isolation-resource-domains.md) | Kernel-Level Isolation/Resource Domains (P27) | Accepted | Sandbox | 2026-06-13 |
| [ADR-0059](ADR-0059-filesystem-syscall-confinement.md) | Filesystem/Syscall Confinement (P28) | Accepted | Sandbox | 2026-06-13 |
| [ADR-0060](ADR-0060-modular-deploy-migration-sqlite.md) | Modular Deploy/Migration/SQLite | Accepted | Core | 2026-06-13 |
| [ADR-0061](ADR-0061-sovereign-platform-p2-p3.md) | Sovereign Platform P2-P3 | Accepted | Core | 2026-06-13 |
| [ADR-0062](ADR-0062-phase-omega-consolidation.md) | Phase Ω: Consolidation | Accepted | Core | 2026-06-13 |
| [ADR-0063](ADR-0063-gated-budget-actuation.md) | Gated Governance Budget Actuation (Ω12) | Accepted | Governance | 2026-06-15 |
| [ADR-0064](ADR-0064-sovereign-self-update.md) | Sovereign Self-Update (check-only + signed CLI apply) | Accepted | Security | 2026-06-19 |
| [ADR-0065](ADR-0065-admin-ui-csp-compliant-stack.md) | Modern Admin UI on CSP-Compliant Vendored Stack | Accepted | Security | 2026-06-19 |
| [ADR-0066](ADR-0066-content-polish-layer.md) | Content Polish Layer: CSP-Safe Highlighting, Related Posts, PWA | Accepted | Core | 2026-06-20 |
| [ADR-0067](ADR-0067-enterprise-interfaces.md) | Enterprise Interfaces: Read-Only GraphQL, i18n, Email Templates, Live Stream | Accepted | Core | 2026-06-20 |
| [ADR-0068](ADR-0068-admin-v3-next-gen-ui.md) | Admin v3: Next-Generation Admin & Block Editor (design system, block editor, media library, TOTP 2FA, intelligence) | Accepted | Core | 2026-06-20 |
| [ADR-0069](ADR-0069-admin-v2-retirement-plan.md) | Admin v2 Retirement Plan (staged deprecation gated on v3 parity) | Accepted | Core | 2026-06-20 |
| [ADR-0070](ADR-0070-sovereign-rich-media.md) | Sovereign Rich Media: server-rendered Mermaid→SVG diagrams + privacy-first click-to-load embeds | Accepted | Core | 2026-06-20 |
| [ADR-0071](ADR-0071-theme-studio.md) | Theme Studio: safe token-driven theme editor with a sandboxed template DSL | Accepted | Core | 2026-06-20 |
| [ADR-0072](ADR-0072-tools-plugins-panel.md) | Tools & Plugins Panel | Accepted | Core | 2026-06-21 |
| [ADR-0073](ADR-0073-convert-to-blocks.md) | Convert-to-Blocks (non-destructive import into block editor) | Accepted | Core | 2026-06-21 |
| [ADR-0074](ADR-0074-plugin-interface-spec.md) | Formal Plugin Interface Specification (language-neutral, versioned contract) | Accepted | Security | 2026-06-21 |
| [ADR-0075](ADR-0075-draft-publish-workflow.md) | Draft/Publish Workflow and Public-Surface Isolation | Accepted | Security | 2026-06-22 |
| [ADR-0081](ADR-0081-editor-inline-markdown.md) | Block Editor Inline Rich Text via Markdown (goldmark + bluemonday) | Accepted | Core | 2026-06-25 |
| [ADR-0082](ADR-0082-theme-studio-code-editor.md) | Theme Studio Code Editor, Head Meta, and Import/Export (CSP-safe) | Accepted | Core | 2026-06-25 |
| [ADR-0083](ADR-0083-vayumail-roles-search.md) | VayuMail Role-Based Accounts and Local Mailbox Search | Accepted | Core | 2026-06-25 |
| [ADR-0084](ADR-0084-vayumail-outbound-deliverability.md) | VayuMail Outbound Deliverability — Vetted DKIM Signing + Well-Formed MIME | Accepted | VayuOS | 2026-06-25 |
| [ADR-0085](ADR-0085-vayumail-outbound-smarthost-relay.md) | VayuMail Optional Outbound Smarthost Relay | Accepted | VayuOS | 2026-06-25 |
| [ADR-0086](ADR-0086-theme-whole-site-design.md) | Themes Restyle the Whole Public Site via Real Markup | Accepted | Core | 2026-06-26 |
| [ADR-0087](ADR-0087-subscription-engine-v2.md) | Subscription Engine v2 — Revenue Analytics, Lifecycle & Activity Log | Accepted | Core | 2026-06-26 |

---

## Supersession Chain

| Superseded ADR | Superseded By | Reason |
|----------------|--------------|--------|
| _(none yet)_ | — | — |

---

## Deprecated ADRs

| ADR | Title | Deprecated In | Removal Target |
|-----|-------|--------------|----------------|
| _(none yet)_ | — | — | — |

---

## Proposed / Under Review

| ADR | Title | Author | Opened |
|-----|-------|--------|--------|
| _(none)_ | — | — | — |

---

## ADR Authorship Guidelines

1. **Number sequentially** from the highest existing ADR + 1.
2. **Status header** must be one of: `Proposed` | `Accepted` | `Deprecated` | `Superseded`.
3. **Supersedes field** is required if this ADR replaces an existing one.
4. **Owner** is the bounded context that owns the decision (see `docs/architecture/bounded-contexts.md`).
5. **Update this INDEX** when filing any new ADR.
6. **RFC required** if the ADR changes a Stable interface (see `docs/compatibility/stability-matrix.md`).

---

## ADR Health Metrics

Run to check ADR index completeness:

```bash
# Count ADRs in filesystem vs index
echo "Filesystem:" && ls docs/adr/ADR-*.md | wc -l
echo "Index rows:" && grep -c "ADR-0" docs/adr/INDEX.md
```

Expected: counts should match (± 1 for INDEX.md itself).
