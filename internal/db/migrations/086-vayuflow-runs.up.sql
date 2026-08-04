-- Migration 086: the VayuFlow run trail (ADR-0151 P2).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- IDEMPOTENCY IS THE POINT OF THIS TABLE, not an optimisation on it. The
-- failure operators actually fear is the newsletter that went out twice, and
-- the only durable defence is that a second attempt at the same work cannot
-- create a second run. idempotency_key is UNIQUE and is derived from the flow
-- plus the identity of what triggered it — for an event that is the inbox row,
-- so redelivery of the same event collides on insert rather than executing.
--
-- The uniqueness lives in the SCHEMA rather than in a check-then-insert in Go,
-- because a check-then-insert is a race with a window: two runners, or one
-- runner and a retry, can both read "no such run" before either writes. A
-- UNIQUE index has no window.
--
-- FLOW_VERSION IS RECORDED, NOT JOINED. A run is read against the flow AS IT
-- WAS when it executed. Without the version an operator reading a six-week-old
-- run sees it against today's step list and draws a false conclusion about what
-- ran.
--
-- OWNER_ROLE IS RESOLVED AT RUN TIME AND FROZEN HERE. The flow does not store a
-- role (that would freeze the authority answer), but the RUN must: it is the
-- record of what authority the run actually executed with, and it is what makes
-- "a flow armed by an admin who was later demoted" a visible fact afterwards
-- rather than an inference.
--
-- BUDGET DECLARED AND SPENT SIT SIDE BY SIDE. The interesting column is
-- `writes: 3 / 20`. A spend column without its ceiling means nothing, and a
-- ceiling without its spend hides the flow that is consistently near it.
--
-- STATUS 'interrupted' IS NOT A FAILURE AND NOT A SUCCESS. A run whose process
-- died mid-flight resumes as interrupted and is never retried by a ticker,
-- because a step that already sent mail must not be replayed. Retry is an
-- operator decision.
CREATE TABLE IF NOT EXISTS vayuflow_runs (id TEXT PRIMARY KEY, flow_id TEXT NOT NULL, flow_version INTEGER NOT NULL, idempotency_key TEXT NOT NULL, trigger_cause TEXT NOT NULL, mode TEXT NOT NULL, status TEXT NOT NULL, owner TEXT NOT NULL, owner_role TEXT NOT NULL, budget_max_steps INTEGER NOT NULL, budget_max_writes INTEGER NOT NULL, budget_max_egress INTEGER NOT NULL, spend_steps INTEGER NOT NULL DEFAULT 0, spend_writes INTEGER NOT NULL DEFAULT 0, spend_egress INTEGER NOT NULL DEFAULT 0, steps_json TEXT NOT NULL DEFAULT '[]', error TEXT NOT NULL DEFAULT '', started_at TEXT NOT NULL, finished_at TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS idx_vayuflow_runs_idem ON vayuflow_runs(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_vayuflow_runs_flow ON vayuflow_runs(flow_id, started_at);
CREATE INDEX IF NOT EXISTS idx_vayuflow_runs_status ON vayuflow_runs(status, started_at);
