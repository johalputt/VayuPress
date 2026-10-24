# Observability

What the install records about itself, where each record lives, and how one
request is followed across them. Everything here is in the binary and the
database; nothing is sent anywhere.

## One request, followed

Every request gets a request id at the front of the chain. The same id is on:

- **the request's log line**, written when the response ends;
- **the shield's event**, when VayuShield refused, challenged, slowed or
  counted it (`shield_events.request_id`), and on the site's Security page
  beside each of the newest refusals.

So a refusal a reader reports, by time and page, is found on the Security
page, and its id leads to the log line with the status and the time it took.

## The log

One JSON object a line, on standard error, read with `journalctl -u
vayupress` on a systemd install.

A request's line carries `request_id`, `correlation_id`, `method`, `path`,
`site` (the host it was for), `status` and `latency_ms`, with `component:
"http"`. The client's address and User-Agent are left out unless
`VAYU_DEBUG_REQUESTS=1` is set, for a debugging session only. Other lines
carry their `component`, a `msg`, and on an error its `error` and
`severity`; a value that looks like a password, key or token in an error is
written as `[REDACTED]` (`internal/logging`).

## The shield's record

`shield_events` holds one row for every refusal, challenge, tarpit and rule
match: the site, the request id, the layer and rule that acted, the action
and outcome, the path, the client class and score, the country, the status
answered and how long deciding took in microseconds. The address is stored
only as the shield's salted hash. The rows are written in batches; what is
written and what is not is described in `docs/VAYUSHIELD.md`.

## The audit log

`audit_log` holds who did what to the install: the action, the actor, the
target and the detail, with its time. Triggers refuse every update and
delete of a row, so the log only grows (migration 005).

## Analytics

Page views, events and reading engagement, as `docs/ANALYTICS.md`
defines them. Their own health is the next section's Telemetry.

## System › Monitoring

The console's view of the running process:

- **Now**: the system mode, requests and page drawing at their slowest
  percentiles, the render cache's share, background jobs, and storage.
- **Error budgets**: counted, never acted on without the operator.
- **Where the time goes**: the slowest routes over 15 minutes.
- **Write connection**: the one writer's waits and stalls.
- **Telemetry**: each queue that writes in batches (page views, collected
  events, reading engagement, the shield's events), with what it holds
  against its cap, and what it dropped (full) and lost (refused by the
  database) since boot.

## Metrics and health

`GET /metrics` is Prometheus text, with the install key; the metrics worth
an alert and their runbooks are in `docs/OPERATIONS.md`, as are the health
endpoints (`/health`, `/health/ready` and the rest).
