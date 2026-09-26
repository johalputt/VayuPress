// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/vayukeep"
)

// The Tor world raises its own backups notice — VayuKeep runs there against
// the world's own database — and the notice must lead somewhere the Tor rail
// offers. It linked to a page the Tor app list did not have, so the operator
// who followed it landed outside every app, with no way back but the browser.
func TestTheTorWorldsBackupNoticeLeadsToItsRail(t *testing.T) {
	defer func(v bool) { config.Cfg.OnionMode = v }(config.Cfg.OnionMode)
	config.Cfg.OnionMode = true

	n, ok := backupNotification(vayukeep.Status{}, "", time.Now().UTC())
	if !ok {
		t.Fatal("backups that are not set up raise no notice; the test proves nothing")
	}
	for _, app := range saVisibleApps(saSession(accessAdmin)) {
		for _, sec := range app.Sections {
			if sec.Href == n.Href {
				return
			}
		}
	}
	t.Errorf("in the Tor world the backups notice links %s, which no Tor app offers", n.Href)
}
