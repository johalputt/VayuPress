## Summary

<!-- One paragraph describing what this PR does and why -->

## Type of Change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (requires MAJOR version bump)
- [ ] Documentation update
- [ ] Performance improvement
- [ ] Security fix
- [ ] Governance / RFC implementation

## Governance Checklist

<!-- Required for all PRs -->

- [ ] Code follows SQLite-first doctrine (feature works without Meilisearch)
- [ ] No heavy frontend frameworks in public paths (HTMX + Alpine.js only)
- [ ] No new external dependencies without RFC approval
- [ ] Backward compatibility maintained (or this is a MAJOR bump with migration guide)
- [ ] No security vulnerabilities introduced
- [ ] No privacy violations (no new telemetry, tracking, or data harvesting)

## Testing

- [ ] `go test -race ./...` passes — zero races
- [ ] `golangci-lint run` passes — zero lint errors
- [ ] `govulncheck ./...` passes — no High/Critical CVEs
- [ ] New code has test coverage ≥ 70% (≥ 80% for critical paths)
- [ ] Integration tests added/updated if applicable
- [ ] `make check-docs` passes — all required docs present

## Documentation

- [ ] `CHANGELOG.md` updated
- [ ] Relevant docs updated (API-REFERENCE, ARCHITECTURE, INSTALLATION, etc.)
- [ ] New configuration options documented in `docs/INSTALLATION.md`
- [ ] ADR created in `docs/adr/` if this is a significant architectural decision

## Performance

- [ ] No >5% regression in p95 latency, memory, or CPU (benchmarks attached if applicable)
- [ ] No new blocking operations on the request path

## Ethical Review

- [ ] No user data involved without explicit opt-in
- [ ] No AI features added without Ethical AI Charter compliance
- [ ] Accessibility not degraded (WCAG 2.2 AA)
- [ ] No dark patterns introduced

## DCO Sign-Off

<!-- All commits must be signed with `git commit -s` (Developer Certificate of Origin) -->

By submitting this PR, I confirm that my contribution is made under the terms of the [MIT License](LICENSE) and I have signed all commits with `git commit -s`.

---

**Related Issues**: Closes #
**RFC**: (link if applicable)
