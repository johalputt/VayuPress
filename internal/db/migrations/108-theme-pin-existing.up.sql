-- Migration 108: Halcyon becomes the theme a new install starts on, and no
-- existing install moves. Until now an install that never saved a theme had no
-- row here and showed Default(); theme.Load now returns Halcyon for a missing
-- row. So, once, every install that already has a user or a post and no saved
-- theme gets the row it was implicitly showing: Default() exactly as
-- theme.Default returns it (the store's archetype and font options are not
-- added, because that install never had them). A fresh database runs this
-- before its first user exists, inserts nothing, and starts on Halcyon.
-- theme.TestMigration108PinsDefaultExactly holds this JSON to Default().
INSERT INTO theme_tokens(id,name,tokens) SELECT 1,'Default','{"Name":"Default","BgDark":"#0a0f1a","SurfaceDark":"#111827","TextDark":"#e5e7eb","MutedDark":"#7a8290","AccentDark":"#2dd4bf","Accent2Dark":"#f59e0b","HiDark":"#fbbf24","GreenDark":"#34d399","BgLight":"#f8fafc","SurfaceLight":"#ffffff","TextLight":"#111827","MutedLight":"#6b7280","AccentLight":"#0b8176","Accent2Light":"#d97706","HiLight":"#b45309","FontSans":"system-ui,-apple-system,BlinkMacSystemFont,''Segoe UI'',Roboto,Helvetica,Arial,sans-serif","FontMono":"ui-monospace,SFMono-Regular,''SF Mono'',Menlo,Consolas,''Liberation Mono'',monospace","FontSizeBase":"1rem","LineHeight":"1.6","MaxWidth":"72ch","RadiusSm":"0.25rem","RadiusLg":"0.75rem"}' WHERE NOT EXISTS (SELECT 1 FROM theme_tokens) AND (EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM articles));
