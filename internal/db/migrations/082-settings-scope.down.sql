-- Down for 082. The index is dropped; the table is deliberately left alone.
--
-- Reversing the rebuild would mean collapsing (scope,key) back to (key), and
-- every hosted domain's settings would have to go somewhere. There is no
-- correct destination: discarding them loses a client's site configuration,
-- and merging them into the primary overwrites the operator's own with
-- whichever domain happened to sort last. A down migration whose only honest
-- options are "lose data" or "corrupt data" should not run.
DROP INDEX IF EXISTS idx_site_settings_scope;
