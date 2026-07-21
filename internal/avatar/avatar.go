// Package avatar generates deterministic, premium cartoon-face avatars as inline
// SVG — no external assets, no fonts, no runtime deps — so a member or mailbox
// always has a polished picture even before uploading one. The same seed always
// yields the same face (stable across page loads); an optional style index pins a
// specific prebuilt cartoon; gender only nudges the default hair when style is auto.
//
// The output is a self-contained <svg> served as image/svg+xml. It uses only SVG
// presentation attributes and internal gradient paint servers (url(#id) references
// that resolve inside the same document) — never a <style> block, <script>, external
// <image>, or external url(...) fetch — so it is safe to embed as an <img> under a
// strict page CSP.
package avatar

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
)

// A pair holds two related colours (e.g. gradient stops, or base + shade).
type pair = [2]string

// Palettes — cheerful, harmonious, and picked by the seed hash. Gradient pairs
// give the flat backgrounds real depth; the skin/hair pairs shade top→bottom.
var (
	// Background gradient stops (top-left → bottom-right).
	bgPairs = []pair{
		{"#6366f1", "#8b5cf6"}, // indigo → violet
		{"#0ea5e9", "#22d3ee"}, // sky → cyan
		{"#10b981", "#34d399"}, // emerald
		{"#f59e0b", "#fb923c"}, // amber → orange
		{"#f43f5e", "#fb7185"}, // rose
		{"#ec4899", "#f472b6"}, // pink
		{"#8b5cf6", "#a78bfa"}, // violet
		{"#14b8a6", "#2dd4bf"}, // teal
	}
	// Skin tone {top-light, bottom-shade}.
	skinTones = []pair{
		{"#ffd8b1", "#f0b98a"},
		{"#f6c39a", "#e3a374"},
		{"#e6ac7d", "#cf8b57"},
		{"#c88a5c", "#a96c3f"},
		{"#a4673f", "#844f2c"},
		{"#7c4b2c", "#5f371e"},
	}
	// Hair tone {top-highlight, bottom-base}.
	hairTones = []pair{
		{"#4a3a2e", "#241a14"}, // dark brown
		{"#7a4a26", "#4a2c16"}, // brown
		{"#b98a45", "#8a5a2b"}, // light brown
		{"#f0c65a", "#d19a2e"}, // blonde
		{"#8a8a8a", "#5b5b5b"}, // grey
		{"#33363c", "#111214"}, // black
		{"#d46a45", "#a83f27"}, // auburn
		{"#f2f2f5", "#dcdce2"}, // platinum
	}
	// Shirt / shoulders.
	shirtColors = []string{"#3b4a63", "#2f3e57", "#0f766e", "#6d28d9", "#b45309", "#9d174d", "#2563eb", "#374151"}
	// Knit-hat colours (beanie cartoon).
	hatColors = []string{"#dc2626", "#2563eb", "#059669", "#7c3aed", "#d97706"}
)

func seedWords(seed string) [8]uint32 {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(seed))))
	var w [8]uint32
	for i := 0; i < 8; i++ {
		w[i] = binary.BigEndian.Uint32(sum[i*4 : i*4+4])
	}
	return w
}

func pick[T any](list []T, n uint32) T { return list[int(n)%len(list)] }

// hairIndexFor chooses a default hairstyle from gender when the caller did not pin
// a style: female → longer/wavier, male → shorter, neutral → a varied mix. Beanie
// (5) is reserved for the prebuilt cartoons, never an auto avatar.
func hairIndexFor(gender string, h uint32) int {
	switch strings.ToLower(strings.TrimSpace(gender)) {
	case "female", "f", "woman":
		return []int{2, 6, 3, 1}[int(h)%4]
	case "male", "m", "man":
		return []int{0, 0, 1, 4}[int(h)%4]
	default:
		return []int{0, 1, 2, 4, 6}[int(h)%5]
	}
}

// Face renders a premium cartoon avatar SVG for the seed. style >= 0 pins a
// specific prebuilt cartoon (its hair/mouth/accessories are derived from style),
// otherwise the face is derived from the seed and gender.
func Face(seed string, style int, gender string) string {
	w := seedWords(seed)

	// A short id suffix keeps gradient ids unique if two avatars are ever inlined
	// into the same document (normally each is its own <img> document).
	uid := strconv.FormatUint(uint64(w[7])^(uint64(style+1)*0x9E3779B1)&0xffffffff, 16)
	if len(uid) > 8 {
		uid = uid[len(uid)-8:]
	}

	bg := pick(bgPairs, w[0])
	skin := pick(skinTones, w[1])
	hair := pick(hairTones, w[2])
	shirt := pick(shirtColors, w[5])
	hat := pick(hatColors, w[6])

	var hairIdx, mouthIdx int
	var glasses, beard bool
	if style >= 0 {
		// Eight distinct, all-premium prebuilt cartoons.
		combos := []struct {
			hair, mouth    int
			glasses, beard bool
		}{
			{0, 0, false, false}, // short + smile
			{1, 2, true, false},  // side-swept + glasses + grin
			{2, 1, false, false}, // long + gentle
			{3, 0, false, false}, // bun + smile
			{4, 0, false, false}, // afro + smile
			{0, 1, false, true},  // short + beard
			{5, 2, false, false}, // beanie + grin
			{6, 1, true, false},  // wavy + glasses
		}
		c := combos[style%len(combos)]
		hairIdx, mouthIdx, glasses, beard = c.hair, c.mouth, c.glasses, c.beard
		// Colour still varies by seed so two people who both pick cartoon 3 differ.
		bg = pick(bgPairs, w[0]+uint32(style))
	} else {
		hairIdx = hairIndexFor(gender, w[3])
		mouthIdx = int(w[4]) % len(mouthPaths)
		glasses = w[5]%5 == 0
		beard = strings.HasPrefix(strings.ToLower(strings.TrimSpace(gender)), "m") && w[6]%3 == 0
	}

	ref := func(id string) string { return "url(#" + id + uid + ")" }

	var b strings.Builder
	b.Grow(2600)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100" role="img" aria-label="avatar">`)

	// ── Gradient paint servers (all internal, CSP-safe) ─────────────────────────
	b.WriteString(`<defs>`)
	b.WriteString(`<linearGradient id="bg` + uid + `" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="` + bg[0] + `"/><stop offset="1" stop-color="` + bg[1] + `"/></linearGradient>`)
	b.WriteString(`<radialGradient id="gl` + uid + `" cx="0.32" cy="0.22" r="0.9"><stop offset="0" stop-color="#ffffff" stop-opacity="0.32"/><stop offset="0.55" stop-color="#ffffff" stop-opacity="0"/></radialGradient>`)
	b.WriteString(`<linearGradient id="sk` + uid + `" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="` + skin[0] + `"/><stop offset="1" stop-color="` + skin[1] + `"/></linearGradient>`)
	b.WriteString(`<linearGradient id="hr` + uid + `" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="` + hair[0] + `"/><stop offset="1" stop-color="` + hair[1] + `"/></linearGradient>`)
	b.WriteString(`</defs>`)

	// ── Background + soft top-left sheen ────────────────────────────────────────
	b.WriteString(`<rect width="100" height="100" fill="` + ref("bg") + `"/>`)
	b.WriteString(`<rect width="100" height="100" fill="` + ref("gl") + `"/>`)

	// ── Neck + shoulders (shirt) ────────────────────────────────────────────────
	b.WriteString(`<path d="M43 62h14v10q-7 4 -14 0z" fill="` + ref("sk") + `"/>`)
	b.WriteString(`<path d="M8 100C8 82 27 73 50 73s42 9 42 27z" fill="` + shirt + `"/>`)
	b.WriteString(`<path d="M8 100C8 82 27 73 50 73c-9 6 -12 15 -12 27z" fill="#ffffff" fill-opacity="0.06"/>`)

	// ── Hair behind the head (long / afro / wavy) ───────────────────────────────
	b.WriteString(hairBack(hairIdx, ref("hr")))

	// ── Ears + head ─────────────────────────────────────────────────────────────
	b.WriteString(`<circle cx="26" cy="49" r="5" fill="` + ref("sk") + `"/><circle cx="74" cy="49" r="5" fill="` + ref("sk") + `"/>`)
	b.WriteString(`<ellipse cx="50" cy="46" rx="25" ry="27" fill="` + ref("sk") + `"/>`)

	// ── Beard (drawn before the mouth so the mouth reads on top) ─────────────────
	if beard {
		b.WriteString(`<path d="M27 50c0 15 10 26 23 26s23-11 23-26c-3 8 -11 12 -23 12s-20-4 -23-12z" fill="` + ref("hr") + `"/>`)
	}

	// ── Hair over the forehead ──────────────────────────────────────────────────
	b.WriteString(hairFront(hairIdx, ref("hr"), hat))

	// ── Brows ───────────────────────────────────────────────────────────────────
	b.WriteString(`<path d="M34 38q6 -3.5 12 -1.4M54 36.6q6 -2 12 1.4" stroke="` + hair[1] + `" stroke-width="2.3" fill="none" stroke-linecap="round"/>`)

	// ── Eyes: white sclera + iris + catchlight (the big lift over dot eyes) ──────
	b.WriteString(`<ellipse cx="40" cy="46" rx="5.4" ry="6.2" fill="#ffffff"/><ellipse cx="60" cy="46" rx="5.4" ry="6.2" fill="#ffffff"/>`)
	b.WriteString(`<circle cx="41" cy="47" r="3.1" fill="#3a2a20"/><circle cx="59" cy="47" r="3.1" fill="#3a2a20"/>`)
	b.WriteString(`<circle cx="42.2" cy="45.6" r="1.1" fill="#ffffff"/><circle cx="60.2" cy="45.6" r="1.1" fill="#ffffff"/>`)

	// ── Nose hint + cheeks ──────────────────────────────────────────────────────
	b.WriteString(`<path d="M50 49q-2 4.5 0.4 6.2" stroke="` + skin[1] + `" stroke-width="1.5" fill="none" stroke-linecap="round"/>`)
	b.WriteString(`<ellipse cx="34" cy="55" rx="4" ry="2.8" fill="#ff8a80" opacity="0.28"/><ellipse cx="66" cy="55" rx="4" ry="2.8" fill="#ff8a80" opacity="0.28"/>`)

	// ── Mouth ───────────────────────────────────────────────────────────────────
	b.WriteString(mouthPaths[mouthIdx])

	// ── Glasses (on top of everything on the face) ──────────────────────────────
	if glasses {
		b.WriteString(`<g fill="#bfe3ff" fill-opacity="0.18" stroke="#2a2f3a" stroke-width="2"><rect x="31.5" y="40" width="15" height="12.5" rx="6.25"/><rect x="53.5" y="40" width="15" height="12.5" rx="6.25"/></g>`)
		b.WriteString(`<path d="M46.5 45.5h7M31.5 43.5l-5.5 -2M68.5 43.5l5.5 -2" stroke="#2a2f3a" stroke-width="2" fill="none" stroke-linecap="round"/>`)
	}

	b.WriteString(`</svg>`)
	return b.String()
}

// mouthPaths — friendly mouth variants (0 smile, 1 gentle, 2 grin with teeth).
var mouthPaths = []string{
	`<path d="M42 58q8 7 16 0" stroke="#7a3b33" stroke-width="2.8" fill="none" stroke-linecap="round"/>`,
	`<path d="M44 59q6 4 12 0" stroke="#7a3b33" stroke-width="2.6" fill="none" stroke-linecap="round"/>`,
	`<path d="M42 57q8 9 16 0z" fill="#7a3b33"/><path d="M43.6 58.2q6.4 3 12.8 0z" fill="#ffffff"/>`,
}

// hairBack returns the hair layer drawn behind the head (long/afro/wavy frame),
// or "" for styles whose hair sits entirely on top.
func hairBack(idx int, hr string) string {
	switch idx {
	case 2: // long, framing the face
		return `<path d="M19 47C17 25 30 13 50 13s33 12 31 34v25c0-6 -6 -9 -8 -7V42c0-11 -10-17 -23-17s-23 6 -23 17v23c-2-2 -8 1 -8 7z" fill="` + hr + `"/>`
	case 4: // afro silhouette (head covers the centre, leaving a rounded crown)
		return `<g fill="` + hr + `"><circle cx="50" cy="22" r="26"/><circle cx="25" cy="35" r="13"/><circle cx="75" cy="35" r="13"/><circle cx="23" cy="52" r="9"/><circle cx="77" cy="52" r="9"/></g>`
	case 6: // wavy, shoulder-length
		return `<path d="M19 47C17 25 30 13 50 13s33 12 31 34c1 9 2 17 -3 24-1-6 -4-9 -6-7V42c0-11 -10-17 -22-17s-22 6 -22 17v22c-2-2 -5 1 -6 7-5-7 -4-15 -3-24z" fill="` + hr + `"/>`
	default:
		return ""
	}
}

// hairFront returns the hair (or hat) drawn over the forehead.
func hairFront(idx int, hr, hat string) string {
	capHair := `<path d="M24 46C21 25 33 14 50 14s29 11 26 32c-2-11 -9 -19 -26 -19s-24 8 -26 19z" fill="` + hr + `"/>`
	switch idx {
	case 1: // side-swept
		return `<path d="M23 47C21 24 37 13 55 16c14 3 22 15 21 30-3-11 -10-18 -23-18-8 0-16 1-22 7-3 3-6 5-8 12z" fill="` + hr + `"/>`
	case 3: // bun + cap
		return capHair + `<circle cx="50" cy="13" r="8" fill="` + hr + `"/>`
	case 4: // afro: the crown is the back layer; nothing on the forehead
		return ""
	case 5: // knit beanie
		return `<path d="M23 37C23 19 34 11 50 11s27 8 27 26z" fill="` + hat + `"/>` +
			`<rect x="20" y="31" width="60" height="8" rx="4" fill="` + hat + `"/>` +
			`<rect x="20" y="31" width="60" height="8" rx="4" fill="#000000" fill-opacity="0.14"/>` +
			`<circle cx="50" cy="11" r="4" fill="` + hat + `"/>`
	default: // 0 short, 2 long-front, 6 wavy-front all use the neat cap
		return capHair
	}
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
