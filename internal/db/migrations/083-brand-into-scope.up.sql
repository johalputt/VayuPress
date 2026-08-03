-- Migration 083: a domain's branding moves into its own settings scope
-- (ADR-0153 Phase 4).
--
-- The migration runner executes ONE statement per physical LINE.
--
-- WHY. Before ADR-0153 a hosted domain could override exactly six presentational
-- fields, and they were kept in domains.config_json because there was nowhere
-- else to put them: site_settings had no scope. Migration 082 gave it one, so
-- those six fields now have a proper home and config_json holding them as well
-- would be a SECOND store for the same thing.
--
-- Two stores for one concept is precisely the defect this whole ADR is
-- correcting. It produces a page that displays one value and saves another,
-- and an operator who cannot tell which of the two is live — which is how the
-- original complaint started.
--
-- WHAT MOVES. Each secondary domain's brand fields become ordinary settings
-- rows under that domain's scope (the scope key is the domain id, matching
-- settings.Scope.key()). The blob keeps `site` and `limits`, which are
-- operational rather than presentational and are read by the registry itself.
--
-- Only NON-EMPTY values move. A blank brand field meant "inherit the primary"
-- under the old overlay; under ADR-0153 D2 an absent row means "use the product
-- default", and writing empty strings would freeze today's inheritance into
-- explicit blanks — silently changing every site that relied on it.
--
-- INSERT OR IGNORE, not REPLACE: if a scope already holds a value for one of
-- these keys, someone set it deliberately through the scoped settings page and
-- the older blob must not overwrite it.
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'site.name',json_extract(config_json,'$.brand.site_name') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.site_name'),'')<>'';
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'site.tagline',json_extract(config_json,'$.brand.tagline') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.tagline'),'')<>'';
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'site.description',json_extract(config_json,'$.brand.description') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.description'),'')<>'';
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'theme.accent_light',json_extract(config_json,'$.brand.accent_light') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.accent_light'),'')<>'';
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'theme.accent_dark',json_extract(config_json,'$.brand.accent_dark') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.accent_dark'),'')<>'';
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'head.theme_color',json_extract(config_json,'$.brand.theme_color') FROM domains WHERE is_primary=0 AND json_valid(config_json) AND COALESCE(json_extract(config_json,'$.brand.theme_color'),'')<>'';
-- A secondary domain that never had a brand set is now NAMED AFTER ITSELF.
--
-- Found by the pre-release pass. Before ADR-0153 an unbranded secondary domain
-- inherited the primary's name at render time; after it, an unset key resolves
-- to the compiled-in default — which is the PRODUCT's name. So an existing
-- install would have come back up with a client's live site titled "VayuPress",
-- and the operator would have discovered it from the client.
--
-- Isolation is still the rule: the fix is not to reinstate inheritance, it is to
-- pick a default that belongs to the domain. A site nobody has named is most
-- truthfully named after its own hostname, and that value is the domain's own —
-- it is not borrowed from the operator and it does not change when they rename
-- their blog.
--
-- INSERT OR IGNORE, so this never overwrites a name the brand copy above moved
-- in, nor one already set through the scoped settings page.
INSERT OR IGNORE INTO site_settings(scope,key,value) SELECT id,'site.name',host FROM domains WHERE is_primary=0;
