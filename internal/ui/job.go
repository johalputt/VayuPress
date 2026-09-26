// SPDX-License-Identifier: Apache-2.0

package ui

import "strconv"

// Job is heavy work running now, as a Status page shows it: what it is doing,
// how far it has got, and the pace it is going at with the pacer's reason, so
// an operator watching a slow backup sees why it is slow rather than guessing.
type Job struct {
	Title    string // "Copying the database"
	Progress string // "41% · 6.9 of 16.8 GB"
	Percent  int    // 0–100; below 0 when the length is not known yet
	Pace     string // the pacer's level: "Running", "Easing", "Waiting"
	Why      string // the pacer's reason
}

// HTML renders the job. Easing and Waiting are marked in the warning tone:
// the job is yielding, which is the design working, but it is also the answer
// to "why is this taking so long".
func (j Job) HTML() HTML {
	bar := `<progress class="ui-job__bar" aria-label="` + string(Text(j.Title)) + `"></progress>`
	if j.Percent >= 0 {
		p := strconv.Itoa(min(j.Percent, 100))
		bar = `<progress class="ui-job__bar" max="100" value="` + p + `" aria-label="` + string(Text(j.Title)) + `">` + p + `%</progress>`
	}
	why := ""
	if j.Pace != "" {
		tone := "ok"
		if j.Pace != "Running" {
			tone = "warn"
		}
		why = `<div class="ui-job__why"><span class="ui-job__pace"><span class="sa-dot sa-dot--` + tone + `"></span>` +
			string(Text(j.Pace)) + `</span>` + string(Text(j.Why)) + `</div>`
	}
	return HTML(`<div class="ui-job"><div class="ui-job__top"><span class="ui-job__title">` + string(Text(j.Title)) +
		`</span><span class="ui-job__meta">` + string(Text(j.Progress)) + `</span></div>` + bar + why + `</div>`)
}
