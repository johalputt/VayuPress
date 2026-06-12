# Ethical Review Process

**Version**: 1.0.0 (Prompt 12 — Ethics)  
**Governed by**: VayuPress Governance Constitution v6.0, Prompt 12  
**Board contact**: ethics@vayupress.com

---

## Overview

The VayuPress Ethical Review Board (ERB) reviews features, changes, and incidents for alignment with the 8 ethical principles in `ETHICS.md`. Ethical review is not a blocker for routine bug fixes — it applies to new features and architectural changes that affect user privacy, autonomy, or data handling.

---

## Ethical Principles (Summary)

Full text in `ETHICS.md`. The 8 principles are:

1. **Privacy by Design** — Collect minimum data; no hidden tracking
2. **User Autonomy** — Users control their data; no dark patterns
3. **Transparency** — All data handling is documented and visible
4. **No Surveillance** — Zero fingerprinting, zero telemetry
5. **Accessibility** — WCAG 2.1 AA minimum; no exclusion
6. **Sustainability** — Efficient resource use; minimal carbon footprint
7. **Open Source Integrity** — License compliance; no CLA traps
8. **Inclusivity** — Welcoming to all; Code of Conduct enforced

---

## When Ethical Review Is Required

**Always required**:
- Any new endpoint that handles user data
- Changes to authentication or authorization
- New third-party integrations (libraries, services, APIs)
- Media handling changes (new file types, upload flows)
- Comment system changes
- Analytics or logging changes
- New UI patterns (forms, modals, prompts)

**Not required** (routine work):
- Bug fixes that don't change behavior or data handling
- Performance optimizations with no user-facing change
- Documentation updates
- CI/CD configuration changes
- Refactors that don't change external behavior

**When in doubt**: ask ethics@vayupress.com. A 24-hour informal review is always available.

---

## Review Process

### Step 1: Self-Assessment (PR Author)

Before submitting a PR that requires ethical review, the author completes the self-assessment in the PR template:

- [ ] Does this change collect any new user data?
- [ ] Does this change contact any external service?
- [ ] Does this change affect user control or consent?
- [ ] Could this change be used to track or profile users?
- [ ] Does this change introduce dark patterns?
- [ ] Does this change affect accessibility?

If all answers are No: the author marks "No ethical review required" in the PR template and the Community Lead spot-checks within 7 days.

If any answer is Yes: formal ERB review is triggered.

### Step 2: Formal Review Request

Add the `ethics-review` label to the PR, or email ethics@vayupress.com with:
- PR link
- Brief description of the change
- Self-assessment answers
- Proposed mitigations (if applicable)

### Step 3: ERB Review (7 calendar days)

The ERB (3 rotating maintainers) reviews within 7 days:

1. **Primary reviewer** (Ethics Lead) reads the PR and self-assessment
2. **Secondary reviewer** reviews independently
3. ERB discusses async in GitHub issue or email thread
4. ERB reaches decision: **Approve** / **Approve with conditions** / **Request changes** / **Reject**

Decisions require 2/3 ERB majority (2 of 3 members).

### Step 4: Decision Documentation

The ERB posts its decision as a PR comment with:
- Decision (Approve/Reject/etc.)
- Rationale (1-3 paragraphs)
- Any required mitigations (conditions for approval)
- ERB member names

All ERB decisions are logged in `docs/ethics-decisions/` (one file per decision, named by PR number).

### Step 5: Implementation (if approved with conditions)

The author implements required mitigations, updates the PR, and notifies ERB. ERB does a final check (max 48 hours) before approving the PR.

---

## ERB Decision Types

| Decision | Meaning | Next Step |
|----------|---------|-----------|
| **Approve** | Ethically sound as-is | Merge when CI passes |
| **Approve with conditions** | OK after specified changes | Implement mitigations, re-notify ERB |
| **Request changes** | Needs redesign | Author revises approach, new review cycle |
| **Reject** | Fundamentally conflicts with principles | Feature is not built in its current form |

A **Reject** decision can be appealed to the BDFL. The BDFL's ruling is final and is published publicly.

---

## Annual Ethics Metrics

The Ethics Lead publishes an annual report each January covering:

- Total features reviewed by ERB
- Breakdown: Approve / Approve-with-conditions / Request-changes / Reject
- Privacy wins: data minimization improvements made
- Accessibility improvements shipped
- Ethics incidents (any principles violated; remediation taken)
- ERB composition (member rotation)
- Community ethics concerns raised via ethics@vayupress.com

The annual report is published as `docs/ethics-reports/YYYY-ethics-report.md`.

---

## Ethical Incident Response

If a shipped feature is later found to violate ethical principles:

1. **Report** to ethics@vayupress.com or open a GitHub issue
2. **Ethics Lead** acknowledges within 48 hours
3. **ERB convenes** within 7 days for severity assessment
4. **Remediation plan** published within 14 days
5. **Fix shipped** within 30 days for high-severity; 90 days for medium
6. **Post-mortem** published as `docs/ethics-decisions/incident-YYYY-MM-DD.md`

---

## Ethical AI Charter (for AI-assisted features)

If VayuPress ever integrates AI/ML features:

1. No AI system may profile users without explicit opt-in consent
2. AI outputs affecting users must be explainable on request
3. No AI model may be trained on VayuPress user data without explicit consent
4. AI features must have a non-AI fallback
5. AI systems must be audited annually for bias
6. No AI may make autonomous decisions affecting user accounts
7. All AI integrations require ERB approval

VayuPress currently contains **no AI features**. This charter governs future additions.

---

## Contact

- **Ethics Board**: ethics@vayupress.com
- **Ethics Lead**: @johalputt (current)
- **Anonymous reports**: Use GitHub's anonymous issue reporting or email with a throwaway account

All reports are treated confidentially. Retaliation against reporters is a Code of Conduct violation subject to immediate maintainer action.
