# RFC-XXXX: Title

**Status**: Draft | Under Discussion | Accepted | Rejected | Withdrawn  
**Date**: YYYY-MM-DD  
**Author(s)**: @github-handle  
**Prompt alignment**: Prompt N — Area  

---

## Summary

One paragraph explaining what this RFC proposes and why.

---

## Motivation

Why is this change needed? What problem does it solve?
What is the current behavior and why is it inadequate?

---

## Detailed Design

### Proposed Change

Describe the change in detail. Include:
- API changes (new endpoints, changed signatures)
- Database schema changes
- Configuration changes (new env vars)
- UI/UX changes

### Architecture Impact

Does this change affect the SQLite-first doctrine (ADR-0001)?  
Does this require a new ADR?  
What other components are affected?

### Performance Impact

Does this change affect the binary size budget (< 45 MB)?  
Does this change affect memory usage (< 800 MB idle)?  
Include benchmark data if available.

---

## Governance Alignment

| Principle | Impact | Notes |
|-----------|--------|-------|
| Prompt 1 (Architecture) | None / Low / Medium / High | |
| Prompt 2 (Performance) | None / Low / Medium / High | |
| Prompt 3 (UI/UX) | None / Low / Medium / High | |
| Prompt 9 (Security) | None / Low / Medium / High | |
| Prompt 12 (Ethics) | None / Low / Medium / High | |

---

## Ethical Impact Assessment

*(Required for all RFCs — mark N/A if not applicable)*

- Does this RFC collect new user data? If yes, what and why?
- Does this RFC contact any external service?
- Does this RFC affect user control or consent in any way?
- Could this RFC enable tracking or profiling of users?
- Does this RFC affect accessibility?
- Does this RFC align with the Privacy by Design principle?

**Ethical Review Required**: Yes / No  
*(If yes, tag `ethics-review` and email ethics@vayupress.com)*

---

## Alternatives Considered

List other approaches that were evaluated and why they were rejected.

### Alternative 1: [Name]

Description. Why rejected.

### Alternative 2: [Name]

Description. Why rejected.

---

## Implementation Plan

- [ ] Step 1: ...
- [ ] Step 2: ...
- [ ] Step 3: ...
- [ ] New ADR required: Yes / No
- [ ] Documentation changes: list files
- [ ] Migration required: Yes / No

Estimated effort: S / M / L / XL

---

## Open Questions

List any unresolved questions that need community input before acceptance.

1. Question 1?
2. Question 2?

---

## Discussion

*(This section is filled in during the RFC discussion period)*

Comments from community members, maintainers, and leads.

---

## Decision

*(Filled in by Architecture Lead / BDFL after discussion period)*

**Decision**: Accepted / Rejected / Withdrawn  
**Date**: YYYY-MM-DD  
**Decided by**: @maintainer  
**Rationale**: One paragraph.  
**Conditions**: (if any)

---

## References

- Related ADR: [ADR-XXXX](../adr/ADR-XXXX-title.md)
- Related RFC: [RFC-YYYY](RFC-YYYY-title.md)
- Related issues: #XXX
