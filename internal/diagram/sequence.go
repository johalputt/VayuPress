package diagram

import (
	"fmt"
	"regexp"
	"strings"
)

// sequence.go — parser, lifeline-grid layout, and SVG emitter for
// `sequenceDiagram`. Supported: participant declarations; solid (->>) and dashed
// (-->>) messages with labels; and `Note over X` / `Note left of X` annotations.

type seqActor struct {
	id    string
	label string
	x     float64 // centre x of the lifeline
}

type seqMsg struct {
	from, to string
	label    string
	dashed   bool
	isNote   bool
	noteText string
}

var (
	seqParticipantRe = regexp.MustCompile(`(?i)^participant\s+(.+?)(?:\s+as\s+(.+))?$`)
	// A->>B: text   |  A-->>B: text  | A->B: text | A-->B: text
	seqMsgRe  = regexp.MustCompile(`^(\w[\w ]*?)\s*(--?>>?|--?>)\s*(\w[\w ]*?)\s*:\s*(.*)$`)
	seqNoteRe = regexp.MustCompile(`(?i)^note\s+(?:over|left of|right of)\s+([\w ,]+?)\s*:\s*(.*)$`)
)

func renderSequence(src string) (string, error) {
	lines := strings.Split(src, "\n")
	actors := map[string]*seqActor{}
	var order []string
	var msgs []seqMsg

	ensureActor := func(id string) *seqActor {
		id = strings.TrimSpace(id)
		a, ok := actors[id]
		if !ok {
			a = &seqActor{id: id, label: id}
			actors[id] = a
			order = append(order, id)
		}
		return a
	}

	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		if l == "" || strings.HasPrefix(l, "%%") {
			continue
		}
		if strings.EqualFold(l, "sequenceDiagram") {
			continue
		}
		if m := seqParticipantRe.FindStringSubmatch(l); m != nil {
			a := ensureActor(strings.TrimSpace(m[1]))
			if strings.TrimSpace(m[2]) != "" {
				a.label = strings.TrimSpace(m[2])
			}
			continue
		}
		if m := seqNoteRe.FindStringSubmatch(l); m != nil {
			targets := strings.Split(m[1], ",")
			ensureActor(strings.TrimSpace(targets[0]))
			anchor := strings.TrimSpace(targets[0])
			if len(targets) > 1 {
				ensureActor(strings.TrimSpace(targets[len(targets)-1]))
			}
			msgs = append(msgs, seqMsg{from: anchor, to: anchor, isNote: true, noteText: strings.TrimSpace(m[2])})
			continue
		}
		if m := seqMsgRe.FindStringSubmatch(l); m != nil {
			from := ensureActor(m[1]).id
			to := ensureActor(m[3]).id
			dashed := strings.HasPrefix(m[2], "--")
			msgs = append(msgs, seqMsg{from: from, to: to, label: strings.TrimSpace(m[4]), dashed: dashed})
			continue
		}
		// Unrecognised line inside a sequence diagram — ignore (forward-compatible).
	}

	if len(actors) == 0 {
		return "", fmt.Errorf("sequence: no participants parsed")
	}
	return emitSequence(actors, order, msgs), nil
}

const (
	seqActorH    = 38.0
	seqActorPadX = 16.0
	seqColGap    = 40.0
	seqTopPad    = 14.0
	seqMsgGap    = 40.0
	seqMargin    = 16.0
	seqFont      = 14.0
)

func emitSequence(actors map[string]*seqActor, order []string, msgs []seqMsg) string {
	// Column widths from the actor labels.
	x := seqMargin
	var maxW float64
	widths := map[string]float64{}
	for _, id := range order {
		a := actors[id]
		w := approxTextWidth(a.label, seqFont) + 2*seqActorPadX
		if w < 70 {
			w = 70
		}
		widths[id] = w
		if w > maxW {
			maxW = w
		}
	}
	for _, id := range order {
		a := actors[id]
		a.x = x + widths[id]/2
		x += widths[id] + seqColGap
	}
	canvasW := x - seqColGap + seqmargins()
	if canvasW < 120 {
		canvasW = 120
	}

	// Vertical timeline.
	topBoxY := seqMargin
	lifelineTop := topBoxY + seqActorH
	y := lifelineTop + seqTopPad + seqMsgGap

	var body strings.Builder
	for _, m := range msgs {
		if m.isNote {
			a := actors[m.from]
			noteW := approxTextWidth(m.noteText, 12) + 24
			nx := a.x - noteW/2
			fmt.Fprintf(&body, `<rect class="vp-diagram__note" x="%s" y="%s" width="%s" height="26" rx="3" ry="3"></rect>`,
				num(nx), num(y-16), num(noteW))
			fmt.Fprintf(&body, `<text class="vp-diagram__notetext" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="12">%s</text>`,
				num(a.x), num(y-3), esc(m.noteText))
			y += seqMsgGap
			continue
		}
		from, to := actors[m.from], actors[m.to]
		if from == nil || to == nil {
			continue
		}
		dash := ""
		if m.dashed {
			dash = ` stroke-dasharray="6 4"`
		}
		if from.x == to.x {
			// Self message: a small loop.
			fmt.Fprintf(&body, `<path class="vp-diagram__msg" d="M%s,%s C%s,%s %s,%s %s,%s"%s marker-end="url(#vp-arrow)" fill="none"></path>`,
				num(from.x), num(y-10), num(from.x+44), num(y-22), num(from.x+44), num(y+6), num(from.x+4), num(y+4), dash)
		} else {
			fmt.Fprintf(&body, `<line class="vp-diagram__msg" x1="%s" y1="%s" x2="%s" y2="%s"%s marker-end="url(#vp-arrow)"></line>`,
				num(from.x), num(y), num(to.x), num(y), dash)
		}
		if m.label != "" {
			mx := (from.x + to.x) / 2
			fmt.Fprintf(&body, `<text class="vp-diagram__msglabel" x="%s" y="%s" text-anchor="middle" font-size="12">%s</text>`,
				num(mx), num(y-7), esc(m.label))
		}
		y += seqMsgGap
	}
	canvasH := y + seqMargin
	lifelineBottom := canvasH - seqMargin

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" class="vp-diagram vp-diagram--sequence" viewBox="0 0 %s %s" role="img" aria-label="Sequence diagram" preserveAspectRatio="xMidYMid meet">`,
		num(canvasW), num(canvasH))
	b.WriteString(`<defs><marker id="vp-arrow" class="vp-diagram__arrowmarker" markerWidth="10" markerHeight="10" refX="8" refY="3" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L8,3 L0,6 Z" class="vp-diagram__arrowhead"></path></marker></defs>`)

	// Lifelines + actor boxes.
	for _, id := range order {
		a := actors[id]
		fmt.Fprintf(&b, `<line class="vp-diagram__lifeline" x1="%s" y1="%s" x2="%s" y2="%s"></line>`,
			num(a.x), num(lifelineTop), num(a.x), num(lifelineBottom))
		bw := widths[id]
		fmt.Fprintf(&b, `<rect class="vp-diagram__actor" x="%s" y="%s" width="%s" height="%s" rx="6" ry="6"></rect>`,
			num(a.x-bw/2), num(topBoxY), num(bw), num(seqActorH))
		fmt.Fprintf(&b, `<text class="vp-diagram__actorlabel" x="%s" y="%s" text-anchor="middle" dominant-baseline="central" font-size="%s">%s</text>`,
			num(a.x), num(topBoxY+seqActorH/2), num(seqFont), esc(a.label))
	}
	b.WriteString(body.String())
	b.WriteString(`</svg>`)
	return b.String()
}

func seqmargins() float64 { return 2 * seqMargin }
