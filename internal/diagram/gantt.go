package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

// gantt.go — parser and emitter for `gantt` charts. Supported:
// title, dateFormat (ignored; durations parsed as day-counts), sections,
// and tasks: `name : opt-tag, start, dur` where start is either a date-like
// token or `after <id>`, and dur is `<N>d` / `<N>w`. Active / done / crit
// are accepted as status tags.

var ganttTaskRe = regexp.MustCompile(`^([^:]+?)\s*:\s*((?:(?:active|done|crit|milestone),?\s*)*)([^,]*),\s*(.+)$`)
var ganttDurRe = regexp.MustCompile(`(\d+)(d|w)`)
var ganttAfterRe = regexp.MustCompile(`after\s+(\S+)`)

type ganttTask struct {
	id      string
	label   string
	section string
	status  string // done | active | crit | ""
	start   int    // day offset from 0
	dur     int    // days
}

func parseDur(s string) int {
	m := ganttDurRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 1
	}
	n := 0
	fmt.Sscanf(m[1], "%d", &n)
	if m[2] == "w" {
		n *= 7
	}
	if n <= 0 {
		n = 1
	}
	return n
}

func renderGantt(src string) (string, error) {
	var title string
	var tasks []ganttTask
	var section string
	taskEnd := map[string]int{} // id → end day
	taskIdx := 0

	for _, raw := range strings.Split(src, "\n") {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		low := strings.ToLower(l)
		if strings.HasPrefix(low, "gantt") {
			continue
		}
		if strings.HasPrefix(low, "title ") {
			title = strings.TrimSpace(l[6:])
			continue
		}
		if strings.HasPrefix(low, "dateformat") || strings.HasPrefix(low, "axisformat") ||
			strings.HasPrefix(low, "todaymarker") || strings.HasPrefix(low, "excludes") {
			continue
		}
		if strings.HasPrefix(low, "section ") {
			section = strings.TrimSpace(l[8:])
			continue
		}

		m := ganttTaskRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		label := strings.TrimSpace(m[1])
		statusRaw := strings.ToLower(strings.TrimSpace(strings.Trim(m[2], ",")))
		startRaw := strings.TrimSpace(m[3])
		durRaw := strings.TrimSpace(m[4])

		status := ""
		for _, tag := range []string{"done", "active", "crit", "milestone"} {
			if strings.Contains(statusRaw, tag) {
				status = tag
				break
			}
		}

		// Generate a stable id from index if not explicit.
		id := fmt.Sprintf("task%d", taskIdx)
		taskIdx++

		// Compute start day.
		start := 0
		if am := ganttAfterRe.FindStringSubmatch(startRaw); am != nil {
			refID := am[1]
			if end, ok := taskEnd[refID]; ok {
				start = end
			}
		} else {
			// Use current max end as default start (sequential).
			for _, e := range taskEnd {
				if e > start {
					start = e
				}
			}
		}

		dur := parseDur(durRaw)
		taskEnd[id] = start + dur
		tasks = append(tasks, ganttTask{
			id: id, label: label, section: section,
			status: status, start: start, dur: dur,
		})
	}

	if len(tasks) == 0 {
		return "", fmt.Errorf("gantt: no tasks parsed")
	}
	return emitGantt(title, tasks), nil
}

const (
	ganttLabelW  = 130.0
	ganttRowH    = 26.0
	ganttRowGap  = 4.0
	ganttMarginT = 20.0
	ganttMarginB = 16.0
	ganttMarginR = 16.0
	ganttFont    = 12.0
	ganttBarH    = 16.0
)

func emitGantt(title string, tasks []ganttTask) string {
	// Find total span.
	maxDay := 1
	for _, t := range tasks {
		if t.start+t.dur > maxDay {
			maxDay = t.start + t.dur
		}
	}

	titleH := 0.0
	if title != "" {
		titleH = 28.0
	}

	rowCount := len(tasks)
	canvasH := titleH + ganttMarginT + float64(rowCount)*(ganttRowH+ganttRowGap) + ganttMarginB
	chartW := 480.0
	canvasW := ganttLabelW + chartW + ganttMarginR

	dayPx := chartW / float64(maxDay)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--gantt" viewBox="0 0 %s %s" role="img" aria-label="Gantt chart" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))

	if title != "" {
		fmt.Fprintf(&b, `<text class="vp-diagram__title" x="%s" y="%s" text-anchor="middle" font-size="15" font-weight="600">%s</text>`,
			num(canvasW/2), num(ganttMarginT), esc(title))
	}

	// Task rows. Section grouping labels are emitted in the pass below.
	lastSection := ""

	for i, t := range tasks {
		ry := titleH + ganttMarginT + float64(i)*(ganttRowH+ganttRowGap)

		// Task label.
		fmt.Fprintf(&b, `<text class="vp-diagram__gantt-label" x="%s" y="%s" text-anchor="end" dominant-baseline="central" font-size="%s">%s</text>`,
			num(ganttLabelW-6), num(ry+ganttRowH/2), num(ganttFont), esc(t.label))

		// Bar.
		bx := ganttLabelW + float64(t.start)*dayPx
		bw := float64(t.dur) * dayPx
		if bw < 4 {
			bw = 4
		}
		by := ry + (ganttRowH-ganttBarH)/2

		cls := "vp-diagram__gantt-bar"
		switch t.status {
		case "done":
			cls += " vp-diagram__gantt-bar--done"
		case "active":
			cls += " vp-diagram__gantt-bar--active"
		case "crit":
			cls += " vp-diagram__gantt-bar--crit"
		case "milestone":
			cls = "vp-diagram__gantt-milestone"
			mx := bx
			fmt.Fprintf(&b, `<polygon class="%s" points="%s,%s %s,%s %s,%s %s,%s"></polygon>`,
				cls,
				num(mx), num(by+ganttBarH/2),
				num(mx+8), num(by),
				num(mx+16), num(by+ganttBarH/2),
				num(mx+8), num(by+ganttBarH))
			continue
		}
		fmt.Fprintf(&b, `<rect class="%s" x="%s" y="%s" width="%s" height="%s" rx="3"></rect>`,
			cls, num(bx), num(by), num(bw), num(ganttBarH))
	}

	// Section header labels (rendered on top).
	lastSection = ""
	sectionStart := 0
	for i, t := range tasks {
		if t.section != lastSection {
			if lastSection != "" && i > sectionStart {
				sy := titleH + ganttMarginT + float64(sectionStart)*(ganttRowH+ganttRowGap)
				ey := titleH + ganttMarginT + float64(i)*(ganttRowH+ganttRowGap)
				mx := ganttLabelW / 2
				fmt.Fprintf(&b, `<text class="vp-diagram__gantt-section" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="11">%s</text>`,
					num(mx), num((sy+ey)/2), esc(lastSection))
			}
			lastSection = t.section
			sectionStart = i
		}
	}
	if lastSection != "" {
		sy := titleH + ganttMarginT + float64(sectionStart)*(ganttRowH+ganttRowGap)
		ey := titleH + ganttMarginT + float64(len(tasks))*(ganttRowH+ganttRowGap)
		mx := ganttLabelW / 2
		fmt.Fprintf(&b, `<text class="vp-diagram__gantt-section" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="11">%s</text>`,
			num(mx), num((sy+ey)/2), esc(lastSection))
	}

	b.WriteString(`</svg>`)
	return b.String()
}
