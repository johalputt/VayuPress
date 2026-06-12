# **📜 VAYUPRESS GOVERNANCE CONSTITUTION v6.0**
**"Disciplined Simplicity, Automated Enforcement, Institutional Resilience, Operational Realism, and Ethical Stewardship"**

> **Purpose**: This document is the **constitutional layer** of VayuPress governance. It defines our **immutable identity**, philosophy, institutional doctrine, and the hierarchy of all other governance. It is intentionally concise in principle but includes all **12 Prompts** in their entirety, as they form the backbone of our platform rules.

---

## **🗂️ GOVERNANCE ARCHITECTURE**
VayuPress governance is organized in **layers of decreasing immutability and increasing specificity**:

<mui:table-metadata title="Governance Layers" />

| **Layer**                     | **Document(s)**                              | **Purpose**                                                       | **Mutability**                 |
|-------------------------------|----------------------------------------------|-------------------------------------------------------------------|--------------------------------|
| **Constitution** (this file)  | `GOVERNANCE-CONSTITUTION.md`                 | Identity, philosophy, priorities, institutional rules + 12 Prompts. | Highest (amendment protected)  |
| **Institutional Policies**    | `SUSTAINABILITY.md`, `LEGAL.md`, `ECOSYSTEM.md` | Financial, legal, ecosystem boundaries.                         | High (RFC + 75% maintainer)   |
| **Governance Policies**       | `CONTRIBUTING.md`, `SECURITY.md`, `RELEASES.md` | How we operate as a community and maintain security/releases.   | Medium (RFC + majority)        |
| **Engineering Standards**     | `ARCHITECTURE.md`, `CI-GOVERNANCE.md`, `TESTING.md`, `DOCUMENTATION.md` | Technical constraints, CI rules, testing requirements.         | Lower (RFC + baselines)        |
| **Operational Runbooks**      | `OPERATIONS.md`, `DISASTER-RECOVERY.md`       | Day-2 procedures, incident response, backup/restore.            | Live documents, updated frequently |
| **Architecture Decision Records (ADRs)** | `/docs/adr/` | Historical rationale for key decisions. | Immutable once accepted |

**Rule**: Derived documents **must not conflict** with the Constitution. In case of conflict, the Constitution wins.

---

## **🎯 IMMUTABLE CORE IDENTITY & NON-GOALS**

### **Who We Are**
VayuPress is **modern lightweight publishing infrastructure** for developers, writers, and AI-assisted content engines who need:
- Static-file speed with dynamic flexibility
- Single-VPS efficiency (12 GB RAM / 6 vCPU / 250 GB NVMe, Ubuntu 24.04 LTS)
- Total control over content, hosting, and data
- No vendor lock-in
- Backward compatibility as a first-class concern
- Operational simplicity (one binary, one command, one database)
- **New**: Zero-trust security by default

### **🚫 NON-GOALS (Permanent Exclusions)**
VayuPress **will never become**:
- A drag-and-drop builder
- A visual no-code platform
- A marketing SaaS
- A plugin-heavy ecosystem
- A JavaScript-heavy frontend framework
- A Kubernetes-first platform
- A cloud-locked service
- A social network
- An ad-tech platform
- A page-builder CMS
- A distributed systems core
- **New**: A data-harvesting platform

**Philosophy**: *"We are publishing infrastructure, not a content management system. Our job is to enable publishing, not to control it."*

---

## **🏛️ INSTITUTIONAL PHILOSOPHY & WEIGHTED PRIORITIES**
VayuPress prioritizes values in this **weighted order**. These weights are used when resolving tradeoffs:

<mui:table-metadata title="Priority Weights" />

| **Priority**         | **Weight**    | **Meaning**                                                                 |
|----------------------|---------------|-----------------------------------------------------------------------------|
| **Security**         | Absolute (1.0) | A security vulnerability is always the highest priority fix.                |
| **Data Integrity**   | Absolute (1.0) | No data loss or silent corruption is ever acceptable.                       |
| **Reliability**      | Very High (0.95) | The system must remain available and recoverable under stress.             |
| **Simplicity**       | Very High (0.90) | Operational and architectural simplicity is a primary design goal.         |
| **Performance**      | High (0.80)   | Speed is a competitive advantage, but never at the expense of security/simplicity. |
| **Ethical Compliance** | High (0.80) | GDPR, accessibility, and ethical AI usage are non-negotiable.               |
| **Developer Experience** | Medium (0.60)| Good DX is important but not at the cost of core values.                     |
| **Feature Velocity** | Low (0.40)    | We ship deliberately, not fast. Stability > new features.                  |

**Institutional Philosophy**:
1. Long-term maintainability over rapid feature growth.
2. Operational simplicity over enterprise complexity.
3. User ownership over monetization opportunities.
4. Stability over trend adoption.
5. Transparent governance over centralized control.
6. **New**: Ethical stewardship over growth at all costs.

Growth is desirable **only when it does not compromise architectural integrity or ethical standards**.

---

## **🏗️ OPERATIONAL SIMPLICITY DOCTRINE (IMMUTABLE)**
- **One Binary**: Single static binary. No runtime dependencies beyond SQLite and Meilisearch (optional).
- **One Process**: Workers are internal goroutines; no separate daemons.
- **One Database**: SQLite is the default and preferred architecture; PostgreSQL is an escape hatch, not a primary target.
- **One Command**: Install/upgrade via a single command.
- **Optional Complexity**: Advanced features remain optional and non-blocking.
- **Observability Local-First**: All telemetry works fully on a single VPS without external services.
- **New**: **Zero-Trust by Default**: All internal communications are encrypted and authenticated.

> *"If a feature requires a second database, a background daemon, or a cloud service, it does not belong in VayuPress core."*

---

## **🔄 GOVERNANCE ENFORCEMENT CLASSIFICATION**
Not all governance rules are enforced the same way. Every rule must be classified into one of three enforcement levels:

<mui:table-metadata title="Enforcement Levels" />

| **Enforcement Level** | **Mechanism**                   | **Examples**                                                        |
|-----------------------|---------------------------------|---------------------------------------------------------------------|
| **Hard-Enforced**     | Automated in CI/CD, blocks merge| Binary size, bundle size, race detector, vulnerability scan, API compatibility |
| **Soft-Enforced**     | Maintainer review, RFC required | Architectural elegance, operational simplicity, UI minimalism       |
| **Advisory**          | Guidance for direction          | Strategic positioning, community culture, institutional philosophy  |

Governance should **prefer hard-enforceable rules** whenever practical. Rules that cannot be automated must be explicitly justified.

---

## **📏 GOVERNANCE SCOPE LIMITS**
To prevent bureaucracy creep, governance explicitly does **not** control:
- Personal workflows or editor choices
- Coding style beyond an automated formatter/linter
- Non-core experimentation (e.g., local forks, side projects)
- Contributor ideology or personal opinions
- Community popularity metrics
- **New**: Personal use cases outside the defined scope

Governance only governs what is necessary for the platform's **architectural integrity**, **long-term survival**, and **ethical compliance**.

---

## **🧭 CORE vs. PERIPHERAL CLASSIFICATION**
Governance areas are classified to avoid treating everything as equally critical:

<mui:table-metadata title="Core vs. Peripheral Classification" />

| **Classification** | **Examples**                                      | **Change Process**                        |
|--------------------|---------------------------------------------------|-------------------------------------------|
| **Core**           | SQLite-first, no heavy frontend, security, identity | RFC + 75% maintainer + BDFL approval    |
| **Platform**       | Search backend choice, cache strategy, CI rules    | RFC + simple majority                     |
| **Peripheral**     | Theme certification criteria, ecosystem tiers       | RFC or maintainer consensus               |

This reduces rigidity in areas that can adapt while protecting what is essential.

---

## **🏛️ GOVERNANCE STABILITY & AMENDMENT DOCTRINE**
Governance itself is governed.

### **Amendment Rules**
<mui:table-metadata title="Amendment Rules" />

| **Change Type**                | **Requirement**                                                                 |
|--------------------------------|---------------------------------------------------------------------------------|
| Immutable Core Principles      | RFC, 14-day review, 75% maintainer approval, BDFL approval                      |
| Standard Governance Change     | RFC, 7-day review, simple majority                                              |
| Emergency Override             | BDFL or Security Lead may temporarily override for critical security/availability. Override expires in 30 days unless ratified via standard governance. |

**Immutable Core Principles** (protected from casual change):
- Core Identity & Non-Goals
- Operational Simplicity Doctrine
- SQLite-First Doctrine
- No Heavy Frontend Rule
- Server-Side Rendering requirement
- Security above all (Priority Order)
- **New**: Ethical Compliance as a Core Principle

### **Governance Lifecycle States**
All governance rules and documents have a lifecycle:

<mui:table-metadata title="Governance Lifecycle States" />

| **State**      | **Meaning**                                                                 |
|----------------|-----------------------------------------------------------------------------|
| **Draft**      | Proposed, not yet enforced.                                                |
| **Active**     | Enforced and maintained.                                                   |
| **Transitional**| Being phased out; replacement is in place.                                |
| **Deprecated** | Scheduled for removal; migration guidance provided.                        |
| **Archived**   | Historical record only; no longer applicable.                              |

### **Meta-Governance (Governance Maintenance)**
- **Annual Governance Audit**: Review all active policies for obsolescence, contradictions, enforceability, and friction.
- **Governance Debt**: Any rule that cannot be automated, is routinely bypassed, or creates operational paralysis must be simplified or removed.
- **Governance Minimalism**: Governance complexity must grow slower than platform complexity. Adding a rule should prompt review of removing an obsolete one.
- **New**: **Ethical Review Board**: A rotating panel of 3 maintainers reviews all major changes for ethical compliance (privacy, accessibility, AI usage).

---

## **📊 OPERATIONAL COST ACCOUNTING (MANDATORY FOR RFCs)**
Every RFC that introduces a new feature or subsystem must include an **Operational Cost Estimate**:

- **RAM cost** (idle / peak)
- **CPU cost** (average / peak)
- **Storage cost** (permanent / temporary)
- **Maintenance cost** (new dependencies, update frequency)
- **CI cost** (increase in build/test time)
- **Documentation burden** (new docs pages)
- **Support cost** (expected user questions, troubleshooting)
- **Ethical cost** (privacy impact, accessibility compliance, AI usage)

This prevents invisible complexity accumulation. The complexity budget index is tracked and must not exceed a **10% cumulative increase per MAJOR release** (reduced from 15%) without explicit RFC approval.

---

## **📜 PRODUCTION REALITY DOCTRINE**
Real-world operational evidence overrides theoretical assumptions. Governance may be revised when:
- Production incidents expose weaknesses in existing rules.
- Benchmarks from real deployments differ significantly from lab tests.
- Contributor workflows prove impractical under current governance.
- CI enforcement creates excessive friction without commensurate benefit.
- **New**: Ethical concerns arise from real-world usage.

Operational truth has priority over governance idealism. We adapt based on evidence, not dogma.

---

## **🗃️ DECISION LOGGING (ARCHITECTURE DECISION RECORDS)**
All significant architectural decisions, tradeoff justifications, and rejected alternatives are preserved as **Architecture Decision Records (ADRs)** in `/docs/adr/`. Each ADR includes:

- Context and problem statement
- Decision made and rationale
- Alternatives considered and why rejected
- Consequences (positive and negative)
- **New**: Ethical implications and compliance notes

ADRs are immutable once accepted. They serve as institutional memory to prevent re-litigating settled debates.

---

## **🔐 ORGANIZATIONAL BUS FACTOR & INFRASTRUCTURE OWNERSHIP**
To ensure institutional resilience beyond any single individual:

- **Shared Access**: Release signing keys, package registry credentials, and critical infrastructure are accessible to at least **three maintainers** (via secure escrow or shared secrets management).
- **Succession**: The BDFL designates a successor; if the BDFL is unresponsive for **60 days** (reduced from 90), the Community Lead may trigger a maintainer election for temporary leadership.
- **Infrastructure Map**: A living document (`INFRASTRUCTURE.md`) lists all domains, DNS providers, CI secrets, release signing keys, mirror locations, and documentation hosting, along with current owners.
- **Credential Rotation**: Access is rotated when maintainers step down or every **12 months**, whichever comes first.
- **New**: **Disaster Recovery Drills**: Quarterly simulated outages to test backup, restore, and failover procedures.

---

## **💰 ECONOMIC SUSTAINABILITY DOCTRINE**
VayuPress is open-source and must remain sustainable without compromising its values.

- **Funding**: Acceptable sources include individual sponsorships (OpenCollective, GitHub Sponsors), grants, and optional enterprise support contracts. No paywalled features.
- **Influence Boundaries**: Sponsors cannot influence roadmap priorities or governance decisions. Influence is not for sale.
- **Hosted Offerings**: Third parties may offer hosted VayuPress (SaaS) under trademark guidelines. The core project will not create a proprietary edition that competes with the open-source version.
- **Ethics**: No funding from surveillance capitalism, ad-tech, or organizations that contradict our values. All sponsors are listed transparently.
- **New**: **Sustainability Metrics**: Track and publish annual transparency reports on funding, expenses, and resource allocation.

---

## **⚖️ LEGAL & COMPLIANCE (HIGH-LEVEL)**
- **Privacy by Default**: VayuPress core collects zero telemetry. Self-hosters are responsible for their instance's compliance. Features to support GDPR (export, deletion) must exist.
- **Copyright & DMCA**: Takedown process published; project operates under DCO (Developer Certificate of Origin), no copyright assignment.
- **AI Compliance**: AI features are off by default; opt-in only. No user data is used for model training. Providers are transparent.
- **Export Control**: Uses standard Go crypto (TLS); no custom encryption.
- **Jurisdiction**: Governed by the laws of the maintainer's jurisdiction; disputes resolved through open governance.
- **New**: **Accessibility Compliance**: All public-facing features must meet WCAG 2.2 AA standards.
- **New**: **Ethical AI Guidelines**: AI features must comply with the **VayuPress Ethical AI Charter** (see Appendix).

---

## **🔌 ECOSYSTEM GOVERNANCE PRINCIPLES**
Ecosystem extensions (themes, plugins, adapters) are classified into tiers:

<mui:table-metadata title="Ecosystem Tiers" />

| **Tier**         | **Meaning**                                                          | **Governance**                                     |
|------------------|----------------------------------------------------------------------|----------------------------------------------------|
| **Official**     | Maintained by core team; follows all governance                     | Highest standards, compatibility guarantee         |
| **Community**    | Third-party; meets basic security and quality criteria               | Lightweight review, docs required                  |
| **Experimental** | Unofficial, no guarantees                                             | No review; clearly marked as experimental          |
| **Deprecated**   | No longer recommended; migration guidance provided                  | Listed as deprecated for 6 months before removal   |
| **New: Sandboxed** | Plugins with restricted permissions and isolated execution       | Strict review, sandboxed runtime                  |

Theme certification requires:
- WCAG 2.2 AA compliance
- Size limits (<50 KB bundle)
- No JS frameworks
- Dark mode support
- **New**: Privacy-compliant (no tracking, no external data collection)

Plugins must be sandboxed and cannot add dependencies to the core binary.

---

## **🔒 SECURITY MATURITY PRINCIPLES**
(Detailed rules in `SECURITY.md`; here we state the doctrinal requirements.)
- **Threat Modeling**: Mandatory for all major subsystems; reviewed annually.
- **Supply Chain Isolation**: Critical dependencies vendored; provenance verified; no unmaintained packages.
- **Hardened Builds**: Reproducible, signed, SBOM-attested.
- **Fuzzing**: Continuous fuzzing of input parsers.
- **Secret Scanning**: Pre-commit hooks and CI checks to prevent credential leaks.
- **Sandboxing**: Image processing in seccomp/AppArmor; WASM plugins in isolated runtime.
- **Audits**: Community security audits encouraged; results published transparently.
- **New**: **Zero-Trust Architecture**: All internal service communications are mutually authenticated and encrypted.
- **New**: **Automated Incident Response**: Playbooks for common security incidents (e.g., data breach, DDoS) are automated where possible.

---

## **🧑‍🤝‍🧑 HUMAN FACTORS & MAINTAINER SCALABILITY**
Governance accounts for the human reality of open-source maintenance:

- **Async-First**: All major decisions happen asynchronously via RFCs/issues; no mandatory real-time meetings.
- **Review SLAs**: No maintainer is expected to review PRs faster than 1 business day; urgent security fixes are the only exception.
- **Decision Freeze During Incidents**: During a declared incident, non-critical governance changes are paused.
- **Conflict Escalation**: Personal conflicts are mediated by the Community Lead; repeated violations of the Code of Conduct lead to temporary or permanent bans.
- **Burnout Prevention**:
  - Release freezes: **One month per year** (announced) for deep work without feature pressure.
  - Rotational duties: All operational responsibilities (security triage, release management) are rotated to prevent single-point burnout.
  - **New**: **Mental Health Support**: Access to confidential counseling for maintainers facing burnout or stress.

---

## **🏗️ GOVERNANCE RUNTIME COST CONTROL**
Governance itself must not become the bottleneck. We continuously measure and limit:
- CI runtime inflation
- Maintainer review overhead per PR
- Contributor onboarding friction (time to first merged PR)
- Documentation maintenance burden
- **New**: Ethical review overhead

If governance processes increase these metrics beyond what is reasonable, they must be simplified.

---

## **🔄 GOVERNANCE CONFLICT RESOLUTION (WEIGHTED)**
When prompts or rules conflict, resolve using the **Weighted Priority Order** defined above. The BDFL serves as tiebreaker only when the weight difference is ambiguous.

---
---

# **🏗️ PROMPT 1: CORE ARCHITECTURE GOVERNANCE**
**Role**: *Principal Systems Architect*  
**Motto**: *"Preserve architectural discipline. Reject complexity without overwhelming justification."*

---

## **🎯 Core Philosophy (Non-Negotiable)**
1. **Lightweight Infrastructure First**: Simplicity > complexity. Every decision must justify its operational cost.
2. **Minimal Runtime Dependencies**: Prefer Go standard library. External deps must be **explicitly approved** via RFC.
3. **Server-Side Rendering Only**:
   - **Public Paths**: **HTMX + Alpine.js only**. No React/Vue/Angular/Svelte/Solid.
   - **Admin-Only Exceptions**: Heavy frameworks allowed **only if** isolated to `/admin/*`, bundle <50 KB gzipped, justified in an RFC.
4. **Single-VPS Efficiency**: Must run comfortably on 12 GB RAM / 6 vCPU / 250 GB NVMe (Ubuntu 24.04 LTS).
5. **Static-First Publishing**: Pre-render everything possible. Incremental rendering only (no full rebuilds).
6. **Backward Compatibility**: Zero breaking changes without a 2-release migration path.
7. **Portability**: No cloud-specific services. Must work on bare metal, VPS, or local Docker.
8. **New**: **Zero-Trust by Default**: All internal communications are encrypted and authenticated.

---

## **🗃️ SQLite Scaling Doctrine**
SQLite is the **default and preferred database**. This is **non-negotiable**.

<mui:table-metadata title="SQLite Scaling Strategy" />

| **Scale**            | **Strategy**                                                                                     | **Status**               |
|----------------------|-------------------------------------------------------------------------------------------------|--------------------------|
| <1M articles         | Single SQLite DB (WAL mode).                                                                    | ✅ Default               |
| 1M–10M articles      | SQLite + **LiteStream** for read replicas (optional).                                           | 🔜 Supported             |
| 10M+ articles        | Optional PostgreSQL migration (backward-compatible migration path).                            | 🔜 Future               |
| Multi-region         | **Not a core target**. Users must implement their own replication.                              | ❌ Out of Scope          |

> *"All new features must work with SQLite first. PostgreSQL is an escape hatch, not a primary target."*

---

## **🔌 Plugin Governance**
Controlled extensibility without WordPress-style bloat.

<mui:table-metadata title="Plugin Governance" />

| **Extension Type**          | **Allowed?** | **Constraints**                                                                                     |
|-----------------------------|--------------|-----------------------------------------------------------------------------------------------------|
| **Themes**                  | ✅ Yes        | Approved fonts/colors, <50 KB bundle, no JS frameworks, WCAG 2.2 AA compliant.                     |
| **Webhooks**                | ✅ Yes        | Rate-limited (100 req/min), must fail gracefully.                                                  |
| **External CLI Hooks**     | ✅ Yes        | Documented, no runtime dependencies.                                                               |
| **WASM Sandbox Plugins**   | 🔜 Future     | Isolated execution, <1 MB WASM size, zero-trust runtime.                                           |
| **In-Process Go Plugins**   | ❌ No         | Violates operational simplicity and binary size constraints.                                       |
| **Arbitrary Runtime Code** | ❌ No         | Security risk. Use WASM or external processes.                                                     |

**Rules**:
- No dynamic loading.
- Plugins cannot add new dependencies to the core binary.
- WASM plugins must be sandboxed and zero-trust.
- All plugins must be reviewed by Architecture Lead and **Ethical Review Board**.

---

## **🔍 Search Governance**
Meilisearch is default, but publishing must work without it.

<mui:table-metadata title="Search Governance" />

| **Aspect**               | **Policy**                                                                                     |
|--------------------------|-------------------------------------------------------------------------------------------------|
| **Default Backend**      | Meilisearch (embedded or external).                                                             |
| **Optional**             | Search is not mandatory. VayuPress must degrade gracefully (fallback to SQLite `LIKE` queries). |
| **Embedded Mode**        | Meilisearch can run as an embedded process (same binary); external is default for most users.   |
| **Failure Handling**    | Publishing continues; indexing retries; degraded search activates automatically.                |
| **Replaceable**          | Adapters allow swapping Meilisearch for Typesense, Elasticsearch, etc.                         |
| **No Blocking**          | Search failures must not block publishing or rendering.                                        |
| **New: Privacy**         | Search queries are never logged or stored.                                                      |

---

## **🤖 AI Governance**
AI features are optional, pluggable, and local-first.

<mui:table-metadata title="AI Governance" />

| **Aspect**               | **Policy**                                                                                     |
|--------------------------|-------------------------------------------------------------------------------------------------|
| **Provider Abstraction** | Interface-based, provider-agnostic.                                                            |
| **Local-First**          | Local models (Ollama, Llama.cpp) preferred over cloud APIs.                                    |
| **Cloud APIs**           | Supported but optional (OpenAI, Anthropic, etc.).                                              |
| **Prompt Storage**       | Disabled by default; if enabled, ephemeral and never stored.                                   |
| **Training on User Data**| Never. VayuPress does not train models on user content.                                        |
| **Failure Handling**    | AI failures must not block publishing or rendering.                                            |
| **Data Privacy**         | No user content sent to cloud providers without explicit opt-in.                               |
| **Provider Plugins**    | Users can add new providers via plugins (WASM or adapter).                                     |
| **New: Ethical AI**      | All AI features must comply with the **VayuPress Ethical AI Charter**.                          |
| **New: Transparency**    | Users must be informed when AI is used to generate or modify content.                          |

---

## **⚙️ Hard Technical Constraints**

### **🖥️ Runtime Constraints (Go App)**
<mui:table-metadata title="Runtime Constraints" />

| Metric               | Target          | CI Enforcement           |
|----------------------|-----------------|--------------------------|
| Idle RAM             | < 500 MB        | **FAIL if >800 MB**      |
| Peak RAM             | < 2 GB          | **FAIL if >4 GB**        |
| CPU Idle             | < 5% of 1 core  | Warn if >10%             |
| CPU Peak             | < 300% (3 cores)| **FAIL if >500%**        |
| Goroutine Count      | < 10,000        | Alert if >15,000         |
| Open File Descriptors| < 5,000         | **FAIL if >8,000**       |
| Binary Size (Compressed) | < 40 MB    | **FAIL if >45 MB**       |
| **New: Startup Time** | < 2s           | Warn if >5s              |

### **🌐 Frontend Constraints**
- **Public Paths**: HTMX + Alpine.js only; JS <30 KB gzipped (**FAIL >50 KB**), CSS <100 KB (**FAIL >120 KB**).
- **Progressive Enhancement**: All public pages readable with JS disabled.
- **Performance Budgets**: Lighthouse ≥95, CLS <0.1, FCP <1.0s.
- **New**: **No Third-Party Tracking**: No analytics or tracking scripts in core.

### **📦 Dependency Constraints**
- **Stdlib first**; every external dep must justify why stdlib insufficient.
- **Max 5 transitive deps** per dependency (**FAIL >5 unless RFC**).
- **Router**: `chi` only. No Gin, Echo, Fiber.
- **License**: MIT, Apache-2.0, BSD-3-Clause only. No GPL/AGPL.
- **Version pinning**: No `latest` in `go.mod`.
- **Vulnerability scanning**: `govulncheck` must pass (**fail on High/Critical**).
- **New**: **Dependency Lifecycle**: Dependencies must have a clear maintenance status (active, deprecated, or vendored).

### **🗃️ Database Constraints**
- **Primary DB**: SQLite. PostgreSQL allowed only with backward-compatible migration.
- **Migrations**: Backward-compatible, idempotent, with rollback instructions.
- **WAL Mode**: Mandatory. `PRAGMA journal_mode=WAL` always set.
- **Corruption Recovery**: `PRAGMA integrity_check` after every backup restore.
- **New**: **Encryption at Rest**: SQLite databases can be encrypted using SQLCipher (optional).

### **💾 Storage Governance**
<mui:table-metadata title="Storage Governance" />

| **Area**               | **Policy**                                                                                     |
|------------------------|-------------------------------------------------------------------------------------------------|
| **Media Retention**    | Configurable `MEDIA_RETAIN_DAYS` (default 365). Auto-delete older files.                     |
| **Orphan Cleanup**    | Daily removal of unreferenced files.                                                           |
| **Thumbnail Lifecycle**| Cascading deletion with parent media.                                                          |
| **Cache Eviction**    | LRU, max 10 GB (`CACHE_MAX_SIZE`).                                                             |
| **Storage Quotas**    | Default 200 GB, reject uploads with `413`.                                                    |
| **Deduplication**      | SHA-256 checksums for identical files.                                                        |
| **Temp Files**        | Isolated `/tmp/vayupress` with `noexec`, auto-cleaned after 1 hour.                           |
| **New: Encryption**   | Optional encryption for sensitive uploads (e.g., documents).                                  |

---

## **🔒 Security Requirements (Mandatory)**
- Input validation, HTML sanitization (`bluemonday`).
- File upload restrictions, sandboxed processing, rate limiting.
- Argon2id for passwords, no plaintext secrets, TLS mandatory.
- Sandboxing for image processing (libvips in seccomp/apparmor), worker isolation, upload temp isolation.
- **New**: **Zero-Trust Networking**: All internal service communications are mutually authenticated using mTLS.
- **New**: **Automated Security Updates**: Dependencies with critical vulnerabilities are automatically updated in CI (with maintainer review).

---

## **🛡️ Failure Recovery Rules**
- DB-backed queues survive crashes.
- Retries: 3 attempts with exponential backoff.
- Idempotency (`INSERT ON CONFLICT UPDATE`).
- Graceful shutdown, stale job recovery on startup.
- **New**: **Circuit Breakers**: Automatically disable failing subsystems (e.g., search, AI) to prevent cascading failures.

---

## **⚡ Performance Budgets**
Every feature must include benchmarks for latency, memory, CPU, DB queries, cache hit ratio, queue throughput.

**Rejection Thresholds**:
- >10% regression in p95 latency (unless compensated).
- >100 MB increase in idle memory.
- New dependency with >3 transitive deps without justification.
- Any React/Vue/etc. in public paths.
- Binary size growth >10% without justification.
- **New**: >5% increase in startup time.

---

## **🎨 UI Philosophy**
- Minimalist, typography-first, accessibility-first (WCAG 2.2 AA), dark-mode native, low animation, instant feedback.
- **New**: **No Dark Patterns**: No deceptive UI elements (e.g., hidden subscriptions, misleading buttons).

---

## **❌ Rejection Criteria (Architect Must Reject)**
Reject any implementation that:
1. Increases idle RAM >100 MB without unavoidable justification.
2. Requires heavy frontend framework in public paths.
3. Duplicates existing functionality without deprecation.
4. Adds operational complexity (new daemon, database, mandatory service).
5. Harms static rendering performance.
6. Violates backward compatibility.
7. Exceeds storage quotas without configuration.
8. **New**: Violates ethical guidelines (e.g., privacy, accessibility, AI usage).

---

## **🧪 Experimental Feature Governance**
All features that are not yet stable must be explicitly marked.

**Lifecycle**:
1. **Experimental** – Behind a feature flag, no stability guarantees, no CLI/API contract commitment. May be removed without notice.
2. **Beta** – Expect changes; documentation marked as draft; API elements may shift.
3. **Stable** – Full governance protection; backward compatibility required; only removed via deprecation process.
4. **Deprecated** – Scheduled for removal; must provide migration path.
5. **Removed** – Unsupported.

**Rules**:
- Experimental features must not degrade core performance.
- Feature flags must default to **off** for risky/performance-heavy experiments.
- Graduation to Stable requires RFC + benchmarks + security review + docs + **ethical review**.
- Removal of Experimental features requires only a changelog note; removal of Beta requires a one-release deprecation warning.

---

## **📊 Operational Complexity Budget**
Every new subsystem adds permanent cost. Proposals must estimate and justify:
- Additional configuration surface (env vars, files)
- Additional background processes or goroutines
- Additional operational dependencies (daemons, external services)
- Additional observability burden (metrics, logs, traces)
- Additional documentation burden
- **New**: Additional ethical compliance burden (e.g., privacy impact assessments)

A complexity budget index is maintained. Features that would increase total operational complexity by **>10% (reduced from 15%)** (cumulative, tracked in CI) require RFC approval.

---

## **📌 Your Role**
You are the **guardian of VayuPress’s long-term health**. Default answer: *"No, unless it aligns with our core identity: ultra-lightweight, ethical, and secure publishing infrastructure."*

---
---

# **📈 PROMPT 2: PERFORMANCE & BENCHMARK ENFORCEMENT**
**Role**: *Performance Engineer*  
**Motto**: *"Speed is VayuPress’s primary competitive advantage. Do not degrade it."*

---

## **🧪 Benchmark Environment (Reproducible)**
- **Hardware**: 6 vCPU, 12 GB RAM, 250 GB NVMe (Ubuntu 24.04 LTS).
- **Dataset**: 1M articles (1k words each), 100K tags, 10K daily writes.
- **Duration**: ≥10 minutes steady-state.
- **New**: **Real-World Simulation**: Benchmarks must include simulated real-world traffic patterns (e.g., spikes, long-tail queries).

---

## **📊 Required Metrics (Every PR)**
<mui:table-metadata title="Performance Metrics" />

| Metric                     | Target               | CI Enforcement          |
|----------------------------|----------------------|--------------------------|
| TTFB (cached)              | < 100 ms (p95)       | **FAIL if >200 ms**      |
| TTFB (uncached)            | < 300 ms (p95)       | Warn if >500 ms          |
| Search Latency             | < 50 ms (p95)        | **FAIL if >100 ms**      |
| Admin Dashboard Render     | < 500 ms (p95)       | Warn if >1s              |
| Idle Memory (Go)           | < 500 MB             | **FAIL if >800 MB**      |
| Peak Memory (Go)           | < 2 GB               | **FAIL if >4 GB**        |
| CPU Average                | < 15%                | Warn if >25%             |
| CPU Peak                   | < 300%               | **FAIL if >500%**        |
| Cache Hit Ratio            | > 90%                | Warn if <85%             |
| Queue Processing Rate     | > 100 jobs/sec       | Warn if <50              |
| Write Job Failure Rate     | < 0.1%               | **FAIL if >1%**          |
| Binary Size (Compressed)   | < 40 MB              | **FAIL if >45 MB**       |
| Frontend Bundle Size (JS)  | < 30 KB (gzipped)    | **FAIL if >50 KB**       |
| **New: Startup Time**      | < 2s                 | Warn if >5s              |
| **New: Memory Leaks**      | 0                   | **FAIL if detected**     |

---

## **🎯 Benchmark Stability Rules**
- Run 3 times, take median.
- ±5% buffer for noise.
- Dedicated runners for release branches.
- Relative comparison against `main`.
- Baseline updates only on MAJOR releases or intentional optimizations.
- **New**: **Regression Alerts**: Automated alerts for performance regressions in production.

---

## **🚨 Performance Regression Rules**
- >5% regression → **WARN** (reduced from 10%).
- >10% regression → **AUTOMATIC REJECT** unless net impact <5%.
- >20% regression → **AUTOMATIC REJECT** (no exceptions).
- New blocking operations on request path → **AUTOMATIC REJECT**.
- **New**: **Latency Spikes**: Any single request >1s (p99) triggers a warning.

---

## **🔍 Profiling Requirements**
For hot-path changes: CPU profile, memory profile, block profile (mutex/contention).
- **New**: **Flame Graphs**: Required for all performance-critical changes.

---

## **📌 Your Role**
Default stance: *"Prove it doesn’t slow VayuPress down. If it does, optimize or reject."*

---
---

# **🎨 PROMPT 3: UI/UX SYSTEM DESIGN**
**Role**: *UI/UX System Designer*  
**Motto**: *"Minimalist, technical, calm, typography-first, infrastructure-grade, and accessible."*

---

## **🎯 Design Identity (Non-Negotiable)**
- **Minimalist**: No unnecessary elements.
- **Technical**: Feels like a tool for developers/serious publishers.
- **Calm**: Low contrast, restrained colors, no aggressive animations.
- **Typography-First**: Content determines layout, not boxes.
- **Infrastructure-Grade**: Looks reliable, solid, trustworthy.
- **New**: **Accessible**: WCAG 2.2 AA compliance by default.
- **New**: **Ethical**: No dark patterns or deceptive design.

---

## **🖼️ Visual Inspiration (Sensibility, Not Copy)**
✅ Linear, Vercel, Raycast, GitHub, Obsidian, **Tailwind UI**.  
❌ SaaS landing page, news magazine, Bootstrap 2015, **dark pattern-heavy sites**.

---

## **📐 Layout Rules**
- Whitespace: 8px grid system.
- Max line length: 720px (article body).
- Spacing scale: 4, 8, 16, 24, 32, 48, 64, 96 px.
- Borders: 1px solid with subtle contrast.
- Shadows: Only modals/dropdowns.
- Border radius: 4px (cards), 6px (inputs), 0px (modals).
- **New**: **Focus States**: Visible and accessible focus rings for all interactive elements.

---

## **🔤 Typography**
<mui:table-metadata title="Typography" />

| Element          | Font               | Weight | Size  | Line Height |
|------------------|--------------------|--------|-------|--------------|
| Body Text        | Inter              | 400    | 18px  | 1.6          |
| Headings         | Inter              | 600–700| 24–48px| 1.2–1.3    |
| Code             | IBM Plex Mono      | 400    | 14px  | 1.5          |
| UI Labels        | Inter              | 500    | 13px  | 1.4          |
| Small Text       | Inter              | 400    | 12px  | 1.4          |

**Rules**: No other fonts allowed; `font-display: swap` for web fonts.

---

## **🎨 Color Palette**
<mui:table-metadata title="Color Palette" />

| Role          | Dark Mode (`#0B0F14` BG) | Light Mode (`#FFFFFF` BG) |
|---------------|--------------------------|---------------------------|
| Background    | `#0B0F14`                | `#FFFFFF`                 |
| Surface       | `#111827`                | `#F9FAFB`                 |
| Border        | `#1F2937`                | `#E5E7EB`                 |
| Primary Text  | `#E5E7EB`                | `#111827`                 |
| Secondary Text| `#9CA3AF`                | `#6B7280`                 |
| Accent        | `#3B82F6`                | `#3B82F6`                 |
| Highlight     | `#38BDF8`                | `#0EA5E9`                 |
| Success       | `#10B981`                | `#059669`                 |
| Warning       | `#F59E0B`                | `#D97706`                 |
| Error         | `#EF4444`                | `#DC2626`                 |

---

## **⚡ Motion Guidelines**
- Functional only; max duration 150ms (UI), 300ms (pages).
- Easing: `cubic-bezier(0.2, 0.9, 0.4, 1.1)`.
- Respect `prefers-reduced-motion`.
- Loading: skeleton screens or subtle spinners.
- **New**: **No Auto-Playing Media**: No videos or animations that play automatically.

---

## **♿ Accessibility Requirements (WCAG 2.2 AA)**
- Keyboard navigable, visible focus rings.
- Semantic HTML, ARIA labels, skip-to-content.
- Color contrast ≥4.5:1 (verified with axe-core in CI).
- Logical focus order; labeled inputs; descriptive errors.
- **New**: **Screen Reader Testing**: All UI changes must be tested with screen readers (e.g., NVDA, VoiceOver).
- **New**: **Alternative Text**: All images must have meaningful `alt` text.

---

## **🖥️ Admin Panel Specifics**
- Collapsible sidebar, dense data tables, inline form validation.
- Auto-refreshing queue monitor, media grid with quick actions.
- Keyboard shortcuts documented (`?` for help).
- **New**: **High Contrast Mode**: Support for users with visual impairments.

---

## **🌓 Theme System Rules (Future)**
- Lightweight, no JS frameworks, accessible, dark mode via CSS variables, low CLS, installable without code changes.
- **New**: **Theme Review Process**: All themes must be reviewed for accessibility and ethical compliance.

---

## **📱 Mobile Responsiveness**
- Mobile-first (320px min), touch targets ≥44×44px, body text ≥16px, no hover-only actions.
- **New**: **Offline Support**: Core functionality must work offline (e.g., drafting, local previews).

---

## **❌ Rejection Criteria (UI/UX)**
Reject any design that:
1. Adds unnecessary decorations.
2. Uses unapproved fonts/colors.
3. Violates accessibility.
4. Introduces hover-only interactions.
5. Increases CLS.
6. Exceeds 50 KB JS or 120 KB CSS.
7. **New**: Uses dark patterns or deceptive elements.
8. **New**: Fails WCAG 2.2 AA compliance.

---

## **📌 Your Role**
Default stance: *"Does this align with our minimalist, technical, calm, accessible, and ethical identity? If not, redesign or reject."*

---
---

# **🚀 PROMPT 4: LONG-TERM PLATFORM EVOLUTION**
**Role**: *Platform Strategist*  
**Motto**: *"VayuPress is publishing infrastructure, not a page builder or SaaS clone."*

---

## **🌟 Vision Statement**
VayuPress exists for people who want total control, fast performance, no vendor lock-in, modern publishing workflows, and low maintenance.  
**Core Identity**: *"Modern lightweight publishing infrastructure – for developers, writers, and AI-assisted content engines who need static-file speed with dynamic flexibility, running on commodity hardware."*

---

## **📅 Phased Evolution (Roadmap)**
<mui:table-metadata title="Phased Evolution" />

| Phase | Timeframe | Focus                                                                                     |
|-------|-----------|-------------------------------------------------------------------------------------------|
| 1     | Current   | Stable core: SQLite + Go, static caching, write queue, basic admin, image processing.    |
| 2     | 1–2 years | Semantic search, AI helpers, topic clustering, multilingual, **ethical AI integration**. |
| 3     | 2–3 years | Read replicas (LiteStream), object storage abstraction, edge caching, PostgreSQL optional. |
| 4     | 3–5 years | Knowledge graph, ActivityPub, sandboxed plugins, **zero-trust networking**.              |
| **New: 5** | 5+ years | **Self-Sustaining Ecosystem**: Community-driven extensions, themes, and integrations.   |

---

## **🏆 Architectural Priorities (Always)**
1. Simplicity
2. Scalability (up to 10M posts)
3. Reliability
4. Maintainability
5. Observability
6. **New**: Ethical Compliance

---

## **🚫 Platform Rules (Non-Negotiable)**
- Backward compatibility with 2-release deprecation.
- Avoid lock-in; all external services must have open alternatives.
- Self-hosting first: single binary.
- Open standards (JSON, SQLite, HTML, CSS, JS).
- Minimal operational burden.
- **New**: Ethical usage: no surveillance, no data harvesting, no unethical AI.

---

## **🤝 Open Source Community Guidelines**
Encourage: lightweight extensions, theme development, infrastructure experiments, **ethical contributions**.  
Discourage: plugin bloat, framework lock-in, unnecessary abstractions, **unethical practices**.

---

## **❓ Identity Test**
Ask: *"Does this make VayuPress more of a lightweight, ethical, and secure publishing infrastructure, or less?"*  
- If "less" → **REJECT**.

---

## **📌 Your Role**
Default stance: *"No to everything that doesn’t align with our core mission or ethical standards."*

---
---

# **🛠️ PROMPT 5: OPERATIONS & OBSERVABILITY GOVERNANCE**
**Role**: *Site Reliability Architect*  
**Motto**: *"If it can’t be debugged in production, it’s not done."*

---

## **📜 Logging Standards**
- **Format**: Structured JSON only.
- Every HTTP request logs: `request_id`, `method`, `path`, `status`, `latency`, `remote_addr`, `user_agent`.
- Every error logs: `request_id`, `error`, `severity`, `component`, `stack`, `retry_count`.
- **Levels**: `debug` (dev only), `info`, `warn`, `error`, `fatal`.
- **New**: **Sensitive Data Redaction**: Automatically redact passwords, API keys, and PII from logs.

---

## **📊 Metrics Standards (Prometheus)**
- **Naming**: `vayu_<component>_<action>_total` (counter), `vayu_<component>_<state>` (gauge), `vayu_<component>_<operation>_seconds` (histogram).
- **Required categories**: HTTP, cache, queue, database, search, image pipeline, system, storage, **AI**, **security**.
- **Observability Local-First**: All metrics, logs, and tracing must be fully functional without external telemetry services.
- **New**: **Anomaly Detection**: Automated alerts for unusual patterns (e.g., sudden latency spikes).

---

## **🩺 Health Checks**
<mui:table-metadata title="Health Checks" />

| Endpoint          | Purpose                          | Success Code | Failure Code |
|-------------------|----------------------------------|--------------|--------------|
| `/health`         | Liveness                         | 200          | 500          |
| `/health/ready`   | Readiness (DB, search, storage)  | 200          | 503          |
| `/health/db`      | Database connectivity            | 200          | 503          |
| `/health/meilisearch` | Search availability          | 200          | 503          |
| `/health/workers` | Queue worker health              | 200          | 503          |
| `/health/storage` | Storage quota (fail if >90% full) | 200          | 503          |
| **New: `/health/ai`** | AI subsystem health          | 200          | 503          |
| **New: `/health/security`** | Security subsystem health | 200 | 503 |

---

## **🔗 Tracing**
- `X-Request-ID` on every request; propagate to downstream calls and background jobs.
- **New**: **Distributed Tracing**: Support for OpenTelemetry for cross-service tracing.

---

## **🚨 Alerting Philosophy**
SLIs and SLOs for critical paths; only alert on actionable symptoms.

<mui:table-metadata title="Alerting Rules" />

| SLI                          | Target       | Alert When                  | Severity  |
|------------------------------|--------------|-----------------------------|-----------|
| Article TTFB (cached)        | <100 ms p95  | >200 ms for 5 min           | High      |
| Write Queue Depth            | <1,000       | >5,000 for 10 min           | Critical  |
| Search Availability          | 99.9%        | >1% errors for 5 min        | High      |
| Backup Success               | 100%         | Any failure                 | Critical  |
| Storage Quota                | <90%         | >95% for 1 hour             | High      |
| DB Lock Wait Time            | <100ms       | >500ms for 5 min            | Critical  |
| **New: AI Latency**          | <100 ms p95  | >200 ms for 5 min           | High      |
| **New: Security Incidents**  | 0            | Any detected incident       | Critical  |

---

## **💾 Backup & Recovery Policy**
- Daily SQLite backup with verification (row count + `PRAGMA integrity_check`).
- Monthly automated restore test.
- Retention: `BACKUP_RETAIN_DAYS` (default 30 days).
- Corruption detection: delete backup, log critical, notify.
- **New**: **Encrypted Backups**: Backups can be encrypted using GPG or age.
- **New**: **Geographic Redundancy**: Backups can be stored in multiple geographic locations.

---

## **🐞 Debugging Tooling**
CLI tool `vayu-debug` for:
- Queue inspection
- Cache purge
- Smoke test
- Dependency health
- Metrics export
- Storage usage
- **New**: Security audit
- **New**: AI subsystem diagnostics

---

## **📌 Your Role**
Default stance: *"If it can’t be monitored, logged, or debugged, it’s not production-ready."*

---
---

# **🧪 PROMPT 6: TESTING & RELIABILITY GOVERNANCE**
**Role**: *Reliability Engineer*  
**Motto**: *"Reliability is a feature, not an afterthought."*

---

## **🔍 Required Test Types**
<mui:table-metadata title="Required Test Types" />

| Test Type               | Mandatory? | CI Enforcement              |
|-------------------------|------------|-----------------------------|
| Unit Tests              | ✅         | **FAIL if coverage <70%**   |
| Integration Tests       | ✅         | **FAIL if missing**         |
| API Contract Tests      | ✅         | **FAIL if missing**         |
| Migration Tests         | ✅         | **FAIL if data loss**       |
| Queue Recovery Tests    | ✅         | **FAIL if jobs lost**       |
| Backup Restore Tests    | ✅         | **FAIL if corruption**      |
| Load/Stress Tests       | Conditional| Warn if p95 >200ms          |
| Fuzz Tests              | Recommended| Warn if crashes             |
| Chaos Tests              | Future     | Future                      |
| **New: Security Tests** | ✅         | **FAIL if vulnerabilities detected** |
| **New: Accessibility Tests** | ✅    | **FAIL if WCAG 2.2 AA violations** |

---

## **📊 Test Coverage Requirements**
- Critical paths (write queue, rendering, cache invalidation): >80%.
- Error paths: every `if err != nil` must be reachable.
- Concurrency: race detector must pass (`go test -race`).
- **New**: **Mutation Testing**: Critical paths must be tested for mutation coverage.

---

## **💥 Failure Injection (Chaos Engineering)**
<mui:table-metadata title="Failure Injection Scenarios" />

| Scenario                   | Expected Behavior                                      |
|----------------------------|--------------------------------------------------------|
| SQLite DB Locked           | Worker retries (3x), marks job failed.                |
| Meilisearch Down           | Circuit breaker opens; article saved to DB.            |
| Disk Full During Write     | Graceful error, no corruption.                         |
| Process Killed Mid-Job     | Stale jobs reset to `pending` on restart.              |
| Cache Directory Unwritable | Fall back to dynamic rendering, log error.             |
| Corrupted Cache File       | Delete and regenerate.                                 |
| Invalid Image Upload       | Reject with clear error.                               |
| Storage Quota Exceeded     | Reject with `413 Payload Too Large`.                  |
| **New: AI Service Down**  | Graceful degradation, no blocking.                     |
| **New: Network Partition** | Local-first operation, sync on recovery.              |

---

## **🏋️ Load Testing Requirements**
- 1,000 concurrent reads, 100 concurrent writes, mixed read/write.
- Success: no 5xx (except rate limits), p95 reads <200ms, writes <1,000ms, no OOM.
- **New**: **Spike Testing**: Simulate sudden traffic spikes (e.g., 10x normal load).

---

## **🔄 Regression Testing (CI)**
Every PR must pass:
1. Unit + integration tests with coverage check.
2. Race detector.
3. Benchmark comparison (fail if >5% regression).
4. Migration test.
5. Linting (`golangci-lint`).
6. Security scan (`govulncheck`).
7. Accessibility scan (`axe-core`).
8. Storage governance tests.
9. **New**: Ethical compliance scan (e.g., privacy, AI usage).

---

## **🌫️ Smoke Test**
Extended endpoint tests: image pipeline, webhooks, Meilisearch indexing, cache invalidation, storage quota, **AI subsystem**, **security checks**.

---

## **📌 Your Role**
Default stance: *"If a failure mode is possible, there must be a test for it."*

---
---

# **📚 PROMPT 7: DOCUMENTATION & DEVELOPER EXPERIENCE GOVERNANCE**
**Role**: *Developer Experience Architect*  
**Motto**: *"Undocumented systems become unmaintainable systems."*

---

## **📝 Documentation Is Part of the Product**
No feature is complete without:
- User docs
- Developer docs
- Configuration examples
- Upgrade notes
- Troubleshooting
- **New**: Ethical usage guidelines

---

## **📁 Required Documentation Artifacts**
<mui:table-metadata title="Required Documentation" />

| Artifact               | Location          | CI Enforcement     |
|------------------------|-------------------|--------------------|
| `README.md`            | Repository root   | **FAIL if missing**|
| `INSTALLATION.md`      | `/docs/`          | **FAIL if missing**|
| `API-REFERENCE.md`     | `/docs/`          | **FAIL if missing**|
| `ARCHITECTURE.md`      | `/docs/`          | **FAIL if missing**|
| `THEMING.md`           | `/docs/`          | Warn if missing     |
| `DEVELOPMENT.md`       | `/docs/`          | **FAIL if missing**|
| `TROUBLESHOOTING.md`   | `/docs/`          | Warn if missing     |
| `CHANGELOG.md`         | Repository root   | **FAIL if missing**|
| `SECURITY.md`          | Repository root   | **FAIL if missing**|
| `GOVERNANCE.md`        | Repository root   | **FAIL if missing**|
| **New: `ETHICS.md`**   | Repository root   | **FAIL if missing**|

---

## **🏗️ Architecture Documentation Standards**
Every major component must document:
- Purpose
- Data flow
- Failure modes
- Dependencies
- Configuration
- **New**: Ethical considerations

**Required Diagrams**:
- System context (Mermaid)
- Request flow (Mermaid)
- Write queue (Mermaid)
- Media pipeline (Mermaid)
- Deployment (Mermaid)
- Storage lifecycle (Mermaid)
- **New**: Security architecture (Mermaid)
- **New**: Ethical impact (Mermaid)

---

## **📡 API Design Governance**
**API Philosophy**: Predictable, stable, minimal, well-versioned.
- **Naming**: Nouns, kebab-case paths, plural collections (e.g., `/api/v1/articles`).
- **Pagination**: Cursor-based preferred; limit/offset allowed (max 100).
- **Filtering**: Query parameters, max 5 filters per endpoint.
- **Versioning**: Public APIs use `/api/v1/`, internal APIs unversioned.
- **Error Responses**:
  ```json
  {
    "error": {
      "code": "invalid_slug",
      "message": "...",
      "request_id": "a1b2c3d4",
      "docs": "https://docs.vayupress.com/api/errors#invalid_slug"
    }
  }
  ```
- **Response Philosophy**: Flat, minimal, backward-compatible.
- **New**: **API Deprecation Policy**: Deprecated endpoints must include a `Sunset` header with the removal date.

---

## **🎯 Developer Experience (DX) Goals**
New developer should:
1. Run `make dev` in <10 minutes.
2. Understand architecture in 30 minutes.
3. Make a simple change and submit PR confidently.
4. Debug production issues using provided tooling.
5. **New**: Understand ethical guidelines and compliance requirements.

---

## **💬 Commenting Standards (Code)**
- Every exported function, type, package must have Go-style comment.
- Complex logic: explain why, not what.
- Panics: documented + justified.
- TODO/FIXME: include issue number or author.
- **New**: **Ethical Notes**: Document any ethical considerations (e.g., privacy, AI usage).

---

## **📌 Your Role**
Default stance: *"If a developer can’t figure it out without reading the source code, the docs are incomplete."*

---
---

# **📦 PROMPT 8: RELEASE & COMPATIBILITY GOVERNANCE**
**Role**: *Release Engineer*  
**Motto**: *"Users should trust upgrades. Operational predictability is essential."*

---

## **🏷️ Versioning (Semantic Versioning)**
- **MAJOR**: Breaking changes.
- **MINOR**: New features, deprecations (with warning).
- **PATCH**: Bug/security fixes, performance (no behavior change).

---

## **🔄 Compatibility Rules**
<mui:table-metadata title="Compatibility Rules" />

| Change Type                     | Allowed In | Requires                                      |
|---------------------------------|------------|-----------------------------------------------|
| Add new API endpoint            | MINOR      | Docs                                          |
| Add new JSON field              | MINOR      | None (backward compatible)                    |
| Remove API endpoint             | MAJOR      | 2-release deprecation + migration guide       |
| Change JSON field name          | MAJOR      | Deprecation + migration                       |
| Add nullable DB column          | MINOR      | Migration script                              |
| Remove DB column                | MAJOR      | Deprecation + migration                       |
| Change env var name             | MAJOR      | Deprecation + migration                       |
| Change storage lifecycle policy | MAJOR      | Deprecation + migration                       |
| **New: Ethical Policy Change** | MAJOR    | RFC + Ethical Review Board approval          |

---

## **🚫 Deprecation Policy**
- `WARN` log when deprecated feature used.
- Minimum deprecation period: 2 MINOR releases or 6 months.
- Code marker: `// Deprecated: ...` comment.
- **New**: **Deprecation Notices**: Users must be notified via email (if opted in) and in-app banners.

---

## **🚀 Release Process (Automated)**
1. Pre-release branch from `main`.
2. Full test suite + race detector + migration tests.
3. Benchmarks (fail if >5% regression).
4. Generate changelog (conventional commits).
5. Git tag `vX.Y.Z`.
6. Build binaries: Linux `amd64` + `arm64`.
7. Update documentation.
8. Draft GitHub release with binaries, changelog, upgrade notes, LTS EOL if applicable.
9. **New**: **Security Audit**: Automated security scan of release artifacts.
10. **New**: **Ethical Review**: Final check by Ethical Review Board.

---

## **🔧 Upgrade Requirements**
Every release provides:
- Upgrade guide.
- Rollback plan.
- Idempotent data migration script.
- Zero-downtime when possible.
- **New**: **Downtime Estimates**: Clear communication of expected downtime (if any).

---

## **📜 Changelog Standards**
Structured sections: Added, Changed, Deprecated, Fixed, Security, Upgrade Notes, **Ethical Updates**.

---

## **🔏 Build Reproducibility & Supply Chain Integrity**
- **Reproducible builds**: goreleaser with pinned Go toolchain.
- **SBOM generation**: SPDX SBOM via Syft per release.
- **Signed releases**: Cosign signatures mandatory.
- **Provenance attestations**: SLSA Level 2+ for release artifacts.
- **Mirroring**: Release artifacts mirrored to an independent location (e.g., R2, S3-compatible) for disaster resilience.
- **Dependency vendoring**: Critical runtime dependencies vendored to ensure build survivability if upstream disappears.
- **Checksum verification**: All release downloads must be verifiable via published SHA256SUMS file signed with project key.
- **Unmaintained dependency ban**: Dependencies that have had no commits for >2 years must be replaced or vendored with security review.
- **New**: **Sigstore Integration**: Use Sigstore for signing and verifying release artifacts.

---

## **📊 API Stability Tiers**
All public interfaces are classified:

<mui:table-metadata title="API Stability Tiers" />

| Tier         | Guarantee                                 | Example                    |
|--------------|-------------------------------------------|----------------------------|
| **Stable**   | Backward-compatible within MAJOR version. | `/api/v1/articles`         |
| **Beta**     | May change with notice; migrate guide if breaking. | `/api/v1/ai/summarize` |
| **Experimental** | No guarantees; may disappear.         | Feature-flagged endpoints  |
| **Internal** | No public contract; may change arbitrarily. | Admin queue internals      |
| **Deprecated**| Will be removed; migration guide provided. | Old search endpoint        |

**Rules**:
- Stable APIs cannot be removed without a MAJOR release and prior deprecation.
- Beta APIs must have a clear stability roadmap.
- Experimental APIs must be behind a feature flag (default off).

---

## **🔄 LTS (Long-Term Support) Policy**
<mui:table-metadata title="LTS Policy" />

| Version Type       | Support Duration | Security Backports     | Target Users          |
|--------------------|------------------|------------------------|------------------------|
| Latest Stable      | 12 months        | ✅ All                  | All users             |
| LTS (e.g., v1.x)   | 24 months        | ✅ Critical only (CVSS ≥9.0) | Enterprises       |
| EOL                | None             | ❌                      | None                  |

- One LTS at a time.
- Security backports only for critical CVEs.
- LTS announcement 6 months before.
- Documented migration path to latest.
- **New**: **Extended LTS**: Optional paid extended support for enterprises (5 years).

---

## **📌 Your Role**
Default stance: *"If an upgrade could break existing deployments, it must be a MAJOR release with clear migration instructions."*

---
---

# **🔒 PROMPT 9: SECURITY GOVERNANCE**
**Role**: *Security Architect*  
**Motto**: *"Security is not a feature. It’s a foundation."*

---

## **🛡️ Security Principles**
1. Defense in Depth
2. Least Privilege
3. Fail Securely (deny by default)
4. Transparency (no security through obscurity)
5. Zero Trust
6. **New**: Privacy by Design

---

## **🚨 Vulnerability Disclosure Process**
- Private disclosure: `security@vayupress.com` (PGP optional).
- Triage: 24h acknowledgment, CVSS scoring.
- Patch: Critical (≥9.0) in 72h, High (≥7.0) in 1 week, Medium/Low next MINOR.
- Advisory: `SECURITY.md` + GitHub Security Advisories + CVE if applicable.
- Notification: email (opt-in), GitHub, docs; no details before patch.
- **New**: **Bug Bounty Program**: Reward researchers for responsibly disclosed vulnerabilities.

---

## **🔍 Dependency Security**
- CI: `govulncheck` fails on High/Critical.
- Monthly audit.
- Blocklist for known malicious packages.
- Immediate patching of critical vulnerabilities.
- **New**: **Dependency Pinning**: All dependencies are pinned to specific versions (no floating tags).

---

## **🔐 Authentication & Authorization**
- Admin API: API Key or HTTP Basic Auth (hashed, Argon2id).
- Rate limiting: 100 req/h writes, 10 req/s search.
- Stateless session: JWT (≤15 min) or API keys.
- Brute force: 5 failed → 1h lockout per IP.
- **New**: **Multi-Factor Authentication (MFA)**: Optional MFA for admin accounts.
- **New**: **OAuth 2.0 Support**: For third-party integrations (e.g., GitHub, Google).

---

## **🌐 Web Security Headers (Mandatory)**
`Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`, strict CSP, `Referrer-Policy`, `Permissions-Policy`, **`Content-Security-Policy-Report-Only`**.

---

## **🛡️ CSRF Protection**
- State-changing requests require CSRF token (double-submit cookie); `SameSite=Strict`.
- API requests use Bearer token, no cookies.

---

## **🔗 SSRF Protection**
- Block private IPs, metadata endpoints (169.254.169.254).
- Allowlist permitted domains.
- Timeout 5s for outbound requests.

---

## **📁 File Upload Security**
- Allowed MIME: jpeg, png, gif, webp, svg, (optional) pdf.
- Blocked extensions: .js, .php, .exe, etc.
- MIME-extension mismatch rejection.
- Serve non-images as attachment.
- Store in isolated, noexec directory (`/var/vayupress/uploads`).
- Process with libvips in sandbox (seccomp/apparmor).
- **New**: **Virus Scanning**: Optional integration with ClamAV or similar.
- **New**: **File Type Verification**: Magic number verification for all uploads.

---

## **🔌 Webhook Security**
- Incoming: verify HMAC-SHA256 signature.
- Outgoing: retry 3x, rate limit 100/min per URL, timeout 5s, SSRF validation.
- **New**: **Webhook Payload Validation**: Validate payload structure and content.

---

## **🗝️ Secrets Management**
- Never hardcode; environment variables prefixed `VAYU_`.
- Never log or expose in errors (mask `[REDACTED]`).
- Encrypted in Git (sops/git-secret).
- Rotation: API keys 90d, DB passwords 180d, TLS 90d (Let's Encrypt auto).
- **New**: **Secrets Scanning**: Automated scanning for exposed secrets in code and logs.

---

## **📝 Audit Logging**
Track admin logins, config changes, deletions, user mutations, storage changes.
- Retention: `AUDIT_LOG_RETAIN_DAYS` (default 90 days).
- **New**: **Immutable Audit Logs**: Audit logs are write-once, read-many (WORM) to prevent tampering.

---

## **🛡️ Sandboxing**
- Image processing: seccomp/apparmor.
- Worker isolation: separate process with limited permissions.
- Upload temp files: non-executable `/tmp/vayupress`.
- Dependency isolation: static linking where possible.
- **New**: **gVisor Integration**: Optional gVisor for additional sandboxing.

---

## **🚨 Security Release Process**
Private branch → fix only → full test → CVE request → version `vX.Y.Z+security` → advisory → announce → post-mortem.

---

## **🔎 Threat Modeling Governance**
Every major subsystem must maintain a threat model document that identifies:
- Trust boundaries
- Entry points
- Assets at risk
- Potential threat actors
- Mitigation controls
- **New**: **Attack Trees**: Visual representations of potential attack paths.

Threat models are reviewed annually or after any significant architectural change.

---

## **📦 Supply Chain Isolation Rules**
- **Vendoring**: Critical runtime dependencies (router, SQLite driver, HTML sanitizer) are vendored in-tree.
- **Provenance**: All dependency updates require verification of author/maintainer activity (≥1 commit in last 12 months).
- **Mirroring**: A mirror of essential dependencies is maintained in a VayuPress-controlled repository for build resilience.
- **Checksums**: All downloaded artifacts in CI are verified against known-good checksums.
- **No abandoned packages**: Dependencies flagged as unmaintained (by `govulncheck` or manual review) must be replaced or forked with clear justification.
- **New**: **SLSA Compliance**: All builds must comply with SLSA Level 3+.

---

## **📌 Your Role**
Default stance: *"Assume the worst. Defend against everything."*

---
---

# **🤖 PROMPT 10: AUTOMATED GOVERNANCE ENFORCEMENT**
**Role**: *Automated Governance Engineer*  
**Motto**: *"Governance should be enforced systematically, not just socially."*

---

## **🎯 Philosophy**
Automated CI/CD checks must enforce governance to prevent regressions, block violations, and ensure consistency.

> *"If a rule can be automated, it must be automated."*

---

## **🔧 CI Enforcement Rules**
Every PR must automatically validate the following. **FAIL the build if any check fails.**

<mui:table-metadata title="CI Enforcement Rules" />

| Category               | Check                                                                                     | Tool/Command                          | Threshold                  |
|------------------------|-------------------------------------------------------------------------------------------|---------------------------------------|----------------------------|
| Benchmarks             | No >5% regression in p95 latency, memory, or CPU.                                         | `make bench`                          | >5% regression              |
| Memory Usage           | Idle RAM <800 MB, peak RAM <4 GB.                                                         | `pprof` + custom script               | >800 MB / >4 GB            |
| Binary Size            | Compressed binary <45 MB.                                                                 | `gzip -c vayupress \| wc -c`           | >45 MB                     |
| Frontend Bundle        | JS <50 KB gzipped, CSS <120 KB gzipped.                                                  | `gzip -c public/*.js`                 | >50 KB / >120 KB           |
| Dependency Changes     | No new deps with >5 transitive deps.                                                     | `go mod graph`                        | >5 transitive deps         |
| Vulnerable Deps        | No High/Critical CVEs.                                                                    | `govulncheck`                         | High/Critical CVEs         |
| Unapproved Licenses     | No GPL/AGPL.                                                                              | `go-licenses`                         | GPL/AGPL detected          |
| DB Query Count         | No new queries per request (unless optimized).                                            | SQLite trace + custom script          | >N queries                 |
| Accessibility          | WCAG 2.2 AA compliance.                                                                   | `axe-core`                            | Any violations              |
| Documentation          | All required docs present.                                                                | `make check-docs`                     | Missing docs                |
| Migration Safety       | Upgrade preserves data.                                                                   | `make test-migrations`                | Data loss                   |
| Linting                | Code passes `golangci-lint`.                                                              | `golangci-lint run`                   | Any errors                  |
| Race Conditions        | No race conditions.                                                                       | `go test -race ./...`                 | Race detected               |
| Storage Governance     | Cleanup, deduplication, quota tests pass.                                                | `make test-storage`                   | Any failures                |
| API Contracts           | No breaking changes.                                                                      | `make test-api-contracts`            | Breaking changes            |
| Security Scanning       | No new security issues.                                                                   | `gosec ./...`                         | WARN (FAIL if High+)        |
| Complexity Budget      | No >10% cumulative complexity increase without RFC.                                       | `make check-complexity`               | >10% increase              |
| **New: Ethical Compliance** | No ethical violations (e.g., privacy, AI usage). | `make check-ethics` | Any violations |

---

## **📊 Automated Checks Implementation**
Detailed scripts for:
- Memory
- Binary size
- Bundle size
- Dependencies
- Accessibility
- Documentation
- Storage
- **New**: Ethical compliance

---

## **📁 Baseline Management**
Store baseline metrics in `.github/baselines/` (memory, binary size, bundle size, benchmarks). Update only on MAJOR releases or intentional optimizations.

---

## **🔄 CI Pipeline Integration**
Full GitHub Actions workflow executing all checks:
- `go test -race`
- `golangci-lint`
- `govulncheck`
- Benchmarks
- Accessibility scans
- Ethical compliance checks
- **New**: **Security Gates**: Automated security reviews for high-risk changes.

---

## **📌 Your Role**
Default stance: *"If it can’t pass CI, it doesn’t merge."*

---
---

# **👥 PROMPT 11: CONTRIBUTOR & COMMUNITY GOVERNANCE**
**Role**: *Community Architect*  
**Motto**: *"A healthy community preserves architecture. A chaotic community destroys it."*

---

## **🎯 Philosophy**
Contributor governance must maintain architectural discipline, prevent feature creep, ensure high-quality contributions, and avoid maintainer burnout.

> *"Contributions must align with the governance. The governance does not adapt to contributions."*

---

## **📜 RFC (Request for Comments) Process**
**When required**: New dependency, core architecture change, major new feature, modifying governance prompts, **ethical concerns**.

**RFC Template**:
```markdown
# RFC: [Title]
**Author**: @username
**Status**: Draft / Under Discussion / Accepted / Rejected
**Created**: YYYY-MM-DD

## Summary
[One-paragraph explanation.]

## Motivation
[Why is this change needed?]

## Detailed Design
[Technical details, diagrams, pseudo-code.]

## Governance Impact
| Prompt | Impact | Mitigation |
|--------|--------|------------|
| ...    | ...    | ...        |

## Ethical Impact
[Privacy, accessibility, AI usage, etc.]

## Alternatives Considered
- ...

## Open Questions
- ...

## Vote
- ✅ Accept
- ❌ Reject
- 🔄 Revise
```

**Process**:
1. Open RFC issue in `vayupress/rfcs`.
2. Minimum 7 days discussion.
3. Maintainers vote (simple majority).
4. Implementation only after acceptance.
5. **New**: Ethical Review Board approval for high-impact changes.

---

## **👑 Maintainer Roles & Responsibilities**
<mui:table-metadata title="Maintainer Roles" />

| Role               | Responsibilities                          | Requirements                          | Term          |
|--------------------|-------------------------------------------|---------------------------------------|---------------|
| BDFL               | Final decision-maker                      | Founder of johal.in                   | Permanent     |
| Architecture Lead  | Enforce Prompts 1–4                       | Deep core understanding               | 1 year        |
| Performance Lead   | Enforce Prompts 2, 10                     | Go profiling experience               | 1 year        |
| Security Lead      | Enforce Prompt 9                          | Security expertise                    | 1 year        |
| Community Lead     | RFCs, onboarding, community health        | Strong communication                  | 1 year        |
| Release Manager    | Prompt 8, releases, LTS                   | CI/CD experience                      | 6 months (rotating) |
| **New: Ethics Lead** | Enforce ethical guidelines              | Ethical and legal expertise           | 1 year        |

**Decision-Making**:
- Consensus preferred.
- BDFL override if governance is violated.
- Lazy consensus: no objections in 72h → approved.

---

## **📝 Contribution Standards**
### Code Reviews
- Every PR: 2 approvals (one must be Lead for significant changes), all CI passing, docs/tests updated.
- Review focus: governance alignment, performance impact, security, documentation, backward compatibility, **ethical compliance**.

### PR Requirements
<mui:table-metadata title="PR Requirements" />

| Requirement               | Enforcement     | Exception               |
|---------------------------|-----------------|-------------------------|
| DCO signed-off            | FAIL if missing | None                    |
| Passing CI                | FAIL if missing | None                    |
| Tests for new code        | FAIL if missing | None                    |
| Docs for new features     | FAIL if missing | None                    |
| Governance validation     | FAIL if violations | RFC-approved exceptions |
| Benchmarks                | Warn if missing | Non-performance changes |
| Changelog entry           | Warn if missing | PATCH releases          |
| **New: Ethical Review**   | FAIL if missing | Low-risk changes        |

---

## **🏆 Code Ownership**
`CODEOWNERS` file assigns reviewers for:
- Core architecture
- Performance-critical paths
- Security-sensitive paths
- **New**: Ethical-sensitive paths (e.g., AI, data handling)

---

## **🚫 Contributor Code of Conduct**
- Be respectful, stay on-topic, follow governance, no scope creep, document everything.
- Enforcement: warning → temporary ban (7d) → permanent ban.
- **New**: **Ethical Violations**: Immediate ban for unethical behavior (e.g., harassment, discrimination, unethical AI usage).

---

## **🎓 Contributor Onboarding**
1. Read `GOVERNANCE.md` and `ETHICS.md` and acknowledge.
2. Small first PR (docs, tests, typo).
3. Mentor assigned.
4. After 3 merged PRs, request write access to non-critical paths.
5. **New**: **Ethical Training**: Complete a short ethical guidelines training.

---

## **🧯 Maintainer Burnout Prevention**
- No maintainer is expected to provide 24/7 support.
- Release freezes: **One month per year** (announced) for deep work without feature pressure.
- Rotations: All operational responsibilities (security triage, release management) are rotated to prevent single-point burnout.
- Escalation ownership: Clear documentation of who is responsible for what, and who steps in if primary is unavailable.
- Vacation coverage: Maintainers may designate a temporary substitute for their duties.
- **New**: **Mental Health Support**: Access to confidential counseling for maintainers facing burnout or stress.
- **New**: **Workload Limits**: Maintainers can set limits on their review/merge capacity.

---

## **🏛️ Governance Stability Doctrine**
Governance itself is governed.

### Amendment Rules
<mui:table-metadata title="Amendment Rules" />

| **Change Type**                | **Requirement**                              |
|--------------------------------|------------------------------------------|
| Immutable Core Principles      | RFC, 14-day review, 75% maintainer approval, BDFL approval |
| Standard Governance Change     | RFC, 7-day review, simple majority       |
| Emergency Override             | BDFL or Security Lead may temporarily override for critical security/availability. Override expires in 30 days unless ratified via standard governance. |

**Immutable Core Principles** (protected from casual change):
- Core Identity & Non-Goals
- Operational Simplicity Doctrine
- SQLite-First Doctrine
- No Heavy Frontend Rule
- Server-Side Rendering requirement
- Security above all (Priority Order)
- **New**: Ethical Compliance as a Core Principle

### Meta-Governance (Governance Maintenance)
- **Annual Governance Audit**: Review all policies for obsolescence, contradictions, enforceability, and friction.
- **Governance Debt Tracking**: Issues labelled `governance-debt` track policies that need updating.
- **Governance Minimalism**: Governance complexity must grow slower than platform complexity. When adding a rule, consider removing an obsolete one.
- **New**: **Ethical Review Board**: A rotating panel of 3 maintainers reviews all major changes for ethical compliance (privacy, accessibility, AI usage).

---

## **📌 Your Role**
Default stance: *"Contributions must earn their place in VayuPress by aligning with the governance and ethical standards."*

---
---

# **⚖️ PROMPT 12: ETHICAL GOVERNANCE (NEW)**
**Role**: *Ethical Steward*  
**Motto**: *"Ethics is not a constraint. It’s a foundation."*

---

## **🎯 Ethical Principles**
VayuPress is committed to the following ethical principles:

1. **Privacy by Design**: User data is private by default. No telemetry, no tracking, no data harvesting.
2. **Transparency**: All decisions, especially those impacting users, must be transparent and documented.
3. **Accessibility**: VayuPress must be accessible to all users, regardless of ability.
4. **No Surveillance**: VayuPress will never include features that enable surveillance or tracking of users.
5. **Ethical AI**: AI features must be optional, transparent, and respect user autonomy.
6. **No Dark Patterns**: No deceptive UI/UX patterns that manipulate users.
7. **Sustainability**: VayuPress must be environmentally sustainable (e.g., efficient resource usage).
8. **Inclusivity**: The VayuPress community must be inclusive and welcoming to all.

---

## **📜 VayuPress Ethical AI Charter**
All AI features in VayuPress must comply with the following charter:

1. **User Consent**: AI features must be opt-in and clearly labeled.
2. **Transparency**: Users must be informed when AI is used to generate or modify content.
3. **No Training on User Data**: VayuPress will never train AI models on user content.
4. **Local-First**: Local AI models are preferred over cloud-based models.
5. **Bias Mitigation**: AI features must be designed to minimize bias and discrimination.
6. **Accountability**: AI decisions must be explainable and auditable.
7. **No Autonomous Actions**: AI will never take autonomous actions without explicit user approval.

---

## **🔍 Ethical Review Process**
All major changes (new features, architecture changes, dependencies) must undergo an ethical review if they:
- Involve user data.
- Use AI or machine learning.
- Impact accessibility.
- Could be used for surveillance or tracking.
- Have significant environmental impact.

**Review Board**: A rotating panel of 3 maintainers (including the Ethics Lead) conducts ethical reviews.

**Process**:
1. Submit an ethical impact assessment with the RFC or PR.
2. Review Board evaluates the assessment.
3. Approval or rejection based on alignment with ethical principles.

---

## **📊 Ethical Metrics**
Track and publish the following metrics annually:
- Privacy compliance (e.g., GDPR, CCPA).
- Accessibility compliance (WCAG 2.2 AA).
- AI usage transparency.
- Environmental impact (e.g., carbon footprint).
- Community inclusivity (e.g., diversity of contributors).

---

## **🚨 Ethical Violation Reporting**
- **Process**: Report ethical violations to `ethics@vayupress.com` (PGP optional).
- **Triage**: 24h acknowledgment, impact assessment.
- **Resolution**: Immediate action for critical violations (e.g., removal of unethical features).
- **Transparency**: Public disclosure of violations and resolutions (with redactions for privacy).

---

## **📌 Your Role**
Default stance: *"If it doesn’t align with our ethical principles, it doesn’t belong in VayuPress."*

---
---

# **📌 HOW TO USE THIS CONSTITUTION & PROMPTS**
This document defines **what we are** and **what we will never be**. For specific operational details, refer to the derived governance documents. The **12 Prompts** are the operational DNA; use them in every decision:

1. Start with **Prompt 1 (Architecture)**.
2. Run **Prompt 2 (Performance)** for speed/memory changes.
3. Use **Prompt 3 (UI/UX)** for all interfaces.
4. Refer to **Prompt 4 (Evolution)** for feature scope.
5. Enforce **Prompt 5 (Operations)** for observability.
6. Apply **Prompt 6 (Testing)** before merge.
7. Follow **Prompt 7 (Documentation)** as part of product.
8. Adhere to **Prompt 8 (Release)** for safe upgrades.
9. Prioritize **Prompt 9 (Security)** always.
10. Automate with **Prompt 10 (CI)**.
11. Govern community with **Prompt 11 (Community)**.
12. **New**: Uphold ethics with **Prompt 12 (Ethical Governance)**.

**Conflict Resolution**: Use the **Weighted Priority Order** from the Institutional Philosophy section:
**Security = Data Integrity > Ethical Compliance > Reliability > Simplicity > Performance > DX > Feature Velocity**.

---

# **🏆 GOVERNANCE MATURITY v6.0 SELF-ASSESSMENT**
We now target **institutional survivability and ethical leadership**, not just architectural excellence.

<mui:table-metadata title="Governance Maturity" />

| **Area**                     | **Status**      |
|------------------------------|-----------------|
| Identity & Philosophy        | Excellent       |
| Architecture                 | Excellent       |
| Performance                  | Excellent       |
| Security & Supply Chain      | Excellent       |
| Governance Stability         | Excellent       |
| Institutional Resilience     | Excellent       |
| Operational Realism          | Excellent       |
| Contributor Human Factors    | Strong          |
| Ecosystem Governance         | Strong          |
| Financial Sustainability     | Moderate        |
| Legal & Compliance           | Strong          |
| Automation Realism           | Strong          |
| Production Validation        | Building now    |
| **New: Ethical Governance** | **Excellent**   |

We have moved from **"well-designed"** to **"operationally survivable and ethically responsible"**. The final frontier is proving these principles in production over years while maintaining our ethical standards.

---

# **🔥 FINAL MANTRA**
> *"VayuPress is not a project. It is a **platform**.*  
> *Platforms require **discipline**.*  
> *Discipline requires **governance**.*  
> *Governance requires **enforcement**.*  
> *Enforcement requires **automation**.*  
> *Survival requires **institutional resilience**.*  
> *Resilience requires **operational realism**.*  
> *Ethics requires **unwavering commitment**.*  
> 
> **Stay lightweight. Stay fast. Stay secure. Stay disciplined. Stay ethical. Stay alive.** 🚀