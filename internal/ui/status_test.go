// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

func TestAStatusPageSaysItsStateBeforeAnythingElse(t *testing.T) {
	out := string(Status(StatusPage{Title: "Backups", Tone: "ok", State: "Backed up 2 h ago, and it restores"},
		Section("Restore points", "", "")))
	if n := strings.Count(out, `data-page-kind=`); n != 1 || !strings.Contains(out, `data-page-kind="status"`) {
		t.Errorf("the page declares %d kinds, want status once:\n%s", n, out)
	}
	state, section := strings.Index(out, "Backed up 2 h ago"), strings.Index(out, "Restore points")
	if state < 0 || section < 0 || state > section {
		t.Errorf("the state sentence does not come before the sections:\n%s", out)
	}
	if !strings.HasSuffix(out, `</section></div>`) {
		t.Errorf("the sections are not inside the page:\n%s", out)
	}
}

// Each tone has its own mark. The sentence says the state in words; the mark
// must still never show a failure with the passing tick.
func TestEachStateToneHasItsOwnMark(t *testing.T) {
	for tone, icon := range map[string]string{"ok": "check", "warn": "warn", "danger": "error", "": "info"} {
		out := string(Status(StatusPage{Title: "T", Tone: tone, State: "S"}))
		if !strings.Contains(out, `<span class="sa-status__mark" aria-hidden="true">`+string(Icon(icon))+`</span>`) {
			t.Errorf("tone %q is not drawn with the %s mark:\n%s", tone, icon, out)
		}
	}
}
