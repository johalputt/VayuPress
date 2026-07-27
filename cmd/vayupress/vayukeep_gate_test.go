package main

import "testing"

func TestVayuKeepPageIsAdminOnly(t *testing.T) {
	for _, p := range []string{"/os/vayukeep", "/os/api/vayukeep/backup", "/os/api/vayukeep/drill", "/os/api/vayukeep/verify"} {
		if lvl := osPathMinLevel(p); lvl < osPathMinLevel("/os/power") {
			t.Errorf("%s requires level %d, less than /os/power (%d) — it exposes backup paths and restore controls", p, lvl, osPathMinLevel("/os/power"))
		}
	}
}
