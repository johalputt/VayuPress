-- Migration 098 (up): indexes for the Shield console page, which on johal.in (710k signatures, 4.3M challenges) took past the 30-second limit and answered 502. Measured on a database of that size: learned-in-24h 0.7 s to 0.1 ms, the review queue 0.7 s to 0.03 ms, the trail's challenge counts 0.4 s to 20 ms. The covering challenge index supersedes idx_challenges_created, which is dropped so the busiest insert path keeps one index on created_at, not two. NOTE: runMigrations executes line-by-line, so keep each statement on ONE line.
CREATE INDEX IF NOT EXISTS idx_signatures_learned ON vayushield_signatures(auto_learned,created_at);
CREATE INDEX IF NOT EXISTS idx_signatures_queue ON vayushield_signatures(operator_verified,auto_learned,confidence DESC,request_count DESC);
CREATE INDEX IF NOT EXISTS idx_challenges_created_outcome ON vayushield_challenges(created_at,outcome);
DROP INDEX IF EXISTS idx_challenges_created;
