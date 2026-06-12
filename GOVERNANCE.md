# VayuPress Governance

This document is the entry-point summary of VayuPress governance.
The authoritative source is [GOVERNANCE-CONSTITUTION.md](GOVERNANCE-CONSTITUTION.md).

## Governance Architecture

| Layer                  | Document(s)                                                         | Mutability              |
|------------------------|---------------------------------------------------------------------|-------------------------|
| Constitution           | `GOVERNANCE-CONSTITUTION.md`                                        | Highest (amendment-protected) |
| Institutional Policies | `SUSTAINABILITY.md`, `LEGAL.md`, `ECOSYSTEM.md`                    | High (RFC + 75%)        |
| Governance Policies    | `CONTRIBUTING.md`, `SECURITY.md`, `RELEASES.md`                    | Medium (RFC + majority) |
| Engineering Standards  | `ARCHITECTURE.md`, `CI-GOVERNANCE.md`, `TESTING.md`                | Lower (RFC + baselines) |
| Operational Runbooks   | `OPERATIONS.md`, `DISASTER-RECOVERY.md`                            | Live documents          |
| ADRs                   | `/docs/adr/`                                                        | Immutable once accepted |

## Priority Order

Security = Data Integrity > Ethical Compliance > Reliability > Simplicity > Performance > DX > Feature Velocity

## Amendment Rules

| Change Type                 | Requirement                                                              |
|-----------------------------|--------------------------------------------------------------------------|
| Immutable Core Principles   | RFC, 14-day review, 75% maintainer approval, BDFL approval              |
| Standard Governance Change  | RFC, 7-day review, simple majority                                       |
| Emergency Override          | BDFL or Security Lead; expires 30 days unless ratified                  |

## Maintainer Roles

| Role              | Responsibilities                           | Term             |
|-------------------|--------------------------------------------|------------------|
| BDFL              | Final decision-maker                       | Permanent        |
| Architecture Lead | Enforce Prompts 1–4                        | 1 year           |
| Performance Lead  | Enforce Prompts 2, 10                      | 1 year           |
| Security Lead     | Enforce Prompt 9                           | 1 year           |
| Community Lead    | RFCs, onboarding, community health         | 1 year           |
| Release Manager   | Prompt 8, releases, LTS                    | 6 months rotating|
| Ethics Lead       | Enforce ethical guidelines                 | 1 year           |

## RFC Process

Required for: new dependencies, core architecture changes, major new features, governance modifications, ethical concerns.

Submit RFCs to the `vayupress/rfcs` repository. Minimum 7-day discussion. Simple majority vote to accept.

## Contact

governance@vayupress.com — rfc@vayupress.com — community@vayupress.com
