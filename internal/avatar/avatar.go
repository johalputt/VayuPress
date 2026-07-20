// Package avatar generates deterministic, friendly cartoon-face avatars as inline
// SVG — no external assets, no fonts, no runtime deps — so a member always has a
// nice picture even before they upload one. The same seed always yields the same
// face (stable across page loads), and an optional style index selects a specific
// prebuilt cartoon; gender only nudges the default hair when the style is auto.
//
// The output is a self-contained <svg> served as image/svg+xml. It uses only SVG
// presentation attributes (fill/stroke) — never a <style> block or script — so it
// is safe to embed as an <img> under a strict page CSP.
package avatar

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// Palettes. Kept deliberately small and cheerful; indices come from the seed hash.
var (
	bgColors   = []string{"#6366f1", "#0ea5e9", "#10b981", "#f59e0b", "#ef4444", "#ec4899", "#8b5cf6", "#14b8a6"}
	skinColors = []string{"#f8d3b0", "#f0c19b", "#e0ac82", "#c68642", "#a56a43", "#8d5524"}
	hairColors = []string{"#2b2320", "#4a2c17", "#7a4a1e", "#b5651d", "#d1a054", "#5b5b5b", "#101010", "#e8e8e8"}
)

// hairPaths are the crown/hair shapes drawn over the head (0 = short, 1 = swept,
// 2 = long, 3 = bun, 4 = bald/none). Coordinates are in the 100×100 viewBox.
var hairPaths = []string{
	`<path d="M26 46C26 30 38 22 50 22s24 8 24 24c0-2-6-10-24-10S26 44 26 46z" fill="%s"/>`,
	`<path d="M26 46C24 28 40 20 54 24c10 3 16 12 16 22-3-8-10-12-20-12-8 0-14 3-18 8-1-2-6-1-6 4z" fill="%s"/>`,
	`<path d="M24 60C22 34 36 20 50 20s28 14 26 40c-2-4-4-8-6-10 2-14-6-24-20-24S32 46 34 60c-2 2-4 6-6 10-2-4-3-7-4-10z" fill="%s"/>`,
	`<circle cx="50" cy="20" r="9" fill="%s"/><path d="M26 46C26 30 38 22 50 22s24 8 24 24c0-2-6-10-24-10S26 44 26 46z" fill="%s"/>`,
	``,
}

// mouthPaths are simple friendly mouths (smile variants).
var mouthPaths = []string{
	`<path d="M41 66q9 8 18 0" stroke="#3a2a22" stroke-width="2.6" fill="none" stroke-linecap="round"/>`,
	`<path d="M42 65q8 10 16 0q-8 4-16 0z" fill="#c65b58"/>`,
	`<path d="M43 67q7 4 14 0" stroke="#3a2a22" stroke-width="2.6" fill="none" stroke-linecap="round"/>`,
}

func seedWords(seed string) [8]uint32 {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(seed))))
	var w [8]uint32
	for i := 0; i < 8; i++ {
		w[i] = binary.BigEndian.Uint32(sum[i*4 : i*4+4])
	}
	return w
}

func pick(list []string, n uint32) string { return list[int(n)%len(list)] }

// hairIndexFor chooses a default hairstyle from gender when the caller did not
// pin a style: female → longer styles, male → shorter, neutral → hash's choice.
func hairIndexFor(gender string, h uint32) int {
	switch strings.ToLower(strings.TrimSpace(gender)) {
	case "female", "f", "woman":
		opts := []int{2, 2, 3, 1}
		return opts[int(h)%len(opts)]
	case "male", "m", "man":
		opts := []int{0, 0, 4, 1}
		return opts[int(h)%len(opts)]
	default:
		return int(h) % len(hairPaths)
	}
}

// Face renders a cartoon avatar SVG for the seed. style >= 0 pins a specific
// prebuilt cartoon (its hair/mouth/skin are derived from style rather than the
// seed hash), otherwise the face is derived from the seed and gender.
func Face(seed string, style int, gender string) string {
	w := seedWords(seed)
	bg := pick(bgColors, w[0])
	skin := pick(skinColors, w[1])
	hairCol := pick(hairColors, w[2])

	var hairIdx, mouthIdx int
	if style >= 0 {
		hairIdx = style % len(hairPaths)
		mouthIdx = (style / len(hairPaths)) % len(mouthPaths)
		// A pinned cartoon still varies colour by seed so two people who both pick
		// "cartoon 3" don't look identical.
		bg = pick(bgColors, w[0]+uint32(style))
	} else {
		hairIdx = hairIndexFor(gender, w[3])
		mouthIdx = int(w[4]) % len(mouthPaths)
	}

	hair := ""
	if p := hairPaths[hairIdx]; p != "" {
		if strings.Count(p, "%s") == 2 {
			hair = fmt.Sprintf(p, hairCol, hairCol)
		} else {
			hair = fmt.Sprintf(p, hairCol)
		}
	}
	mouth := mouthPaths[mouthIdx]

	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100" role="img" aria-label="avatar">`)
	b.WriteString(`<circle cx="50" cy="50" r="50" fill="` + bg + `"/>`)
	// Shoulders/body hint at the bottom for a portrait feel.
	b.WriteString(`<circle cx="50" cy="96" r="26" fill="` + skin + `" opacity="0.9"/>`)
	// Ears + head.
	b.WriteString(`<circle cx="30" cy="55" r="5" fill="` + skin + `"/><circle cx="70" cy="55" r="5" fill="` + skin + `"/>`)
	b.WriteString(`<circle cx="50" cy="54" r="24" fill="` + skin + `"/>`)
	// Eyes + subtle brows + cheeks.
	b.WriteString(`<circle cx="42" cy="53" r="3" fill="#2a201b"/><circle cx="58" cy="53" r="3" fill="#2a201b"/>`)
	b.WriteString(`<path d="M38 47q4-3 8 0M54 47q4-3 8 0" stroke="#3a2a22" stroke-width="1.6" fill="none" stroke-linecap="round"/>`)
	b.WriteString(`<circle cx="38" cy="60" r="3" fill="#e8836f" opacity="0.35"/><circle cx="62" cy="60" r="3" fill="#e8836f" opacity="0.35"/>`)
	b.WriteString(mouth)
	b.WriteString(hair)
	b.WriteString(`</svg>`)
	return b.String()
}

// Auto renders the deterministic, gender-aware avatar for a seed (member email).
func Auto(seed, gender string) string { return Face(seed, -1, gender) }

// CartoonCount is how many prebuilt cartoons the picker offers.
const CartoonCount = 8

// Cartoon renders prebuilt cartoon n (0..CartoonCount-1) for the seed. Colour
// still varies by seed so the same choice looks personal per member.
func Cartoon(n int, seed string) string {
	if n < 0 {
		n = 0
	}
	return Face(seed, n%CartoonCount, "")
}
