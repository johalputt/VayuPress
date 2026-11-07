// SPDX-License-Identifier: Apache-2.0

package main

// update_schema.go — an update's database migrations, applied by the new
// binary while the old one keeps serving.
//
// The server applies pending migrations before it opens its port, and the
// updater used to re-exec into the new binary at once, so a migration that
// reads every post closed the site for as long as it ran: 133 rebuilt an index
// over johal.in's 235k posts for 5 min 43 s, and every request was a 502. Now
// the updater first runs `<new binary> schema apply` as a child. Meanwhile the
// old process keeps answering: reads go on beside the migration (WAL), and
// writes wait behind it, failing after their busy timeout while it holds the
// lock. Once the schema is ready the switch happens, and the new binary starts
// in seconds because nothing is left to apply.
//
// A child that fails puts the previous binary back. Leaving the new one in
// place would hand the same migration to the next restart, with the port
// closed again.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/update"
)

// schemaApplySince is the first release whose binary has `schema apply`. An
// older one does not know the word, so it is not asked.
const schemaApplySince = "3.18.33"

// schemaApplyTimeout bounds the child. Generous: an index over a million
// posts on a slow disk is a matter of tens of minutes, and giving up early
// would only put the old binary back to meet the same migration next time.
const schemaApplyTimeout = 2 * time.Hour

// schemaApply and restartInto are the two steps activateUpdate takes, as
// variables so a test can stand in for the child and the re-exec.
var (
	schemaApply = runSchemaApply
	restartInto = update.ScheduleRestartExec
)

// activateUpdate prepares the database for the binary just installed at
// realPath, then switches to it; or, if the preparation fails, puts the
// previous binary back and stays on this one.
func (a *App) activateUpdate(realPath, newVersion string, histID int64) {
	if update.CompareVersions(newVersion, schemaApplySince) >= 0 {
		logging.LogInfo("update", "preparing the database for "+newVersion+" while v"+Version+" keeps serving")
		ctx, cancel := context.WithTimeout(context.Background(), schemaApplyTimeout)
		err := schemaApply(ctx, realPath)
		cancel()
		if err != nil {
			a.keepThisVersion(realPath, newVersion, histID, err)
			return
		}
	}
	restartInto(realPath, 1500*time.Millisecond, restartCleanup)
}

// keepThisVersion puts the previous binary (<realPath>.bak, kept by the apply)
// back over the new one and records why on the update's history row, which
// the Update page reads.
func (a *App) keepThisVersion(realPath, newVersion string, histID int64, cause error) {
	detail := "The database could not be prepared for " + newVersion + ": " + cause.Error()
	if err := os.Rename(realPath+".bak", realPath); err != nil {
		detail += ". Putting the previous binary back failed too (" + err.Error() + "), so the next restart runs " +
			newVersion + " and applies its migrations before it serves."
	} else {
		detail += ". The previous binary is back in place and v" + Version + " keeps serving."
	}
	logging.LogError("update", detail, "")
	if a.updateStore != nil && histID > 0 {
		_ = a.updateStore.MarkComplete(context.Background(), histID, "failed", detail)
	}
}

// runSchemaApply runs `bin schema apply` with this process's environment, the
// same configuration the server reads, and returns its last words on failure.
func runSchemaApply(ctx context.Context, bin string) error {
	cmd := exec.CommandContext(ctx, bin, "schema", "apply") //nosec G204 -- bin is the binary this update just verified and installed
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 400 {
			tail = "…" + tail[len(tail)-400:]
		}
		return fmt.Errorf("%w: %s", err, tail)
	}
	return nil
}
