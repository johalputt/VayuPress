// SPDX-License-Identifier: Apache-2.0

// Package avatar generates deterministic cartoon-face avatars as inline
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

// A pair holds two related colours (e.g. base + shade).
type pair = [2]string

// Palettes. The backgrounds are quiet, desaturated tints, so the face is the
// subject and a row of avatars reads as one set beside the console's neutral
// surfaces. The earlier set was saturated gradients (indigo to violet, rose,
// amber), which made every picture shout and the faces look like clip-art (3cp).
var (
	// Background {fill, the soft disc behind the head}.
	bgPairs = []pair{
		{"#ECE7F6", "#DDD3F0"}, // lavender
		{"#E1EEE6", "#CBE2D4"}, // sage
		{"#F5EBDD", "#EBD8BF"}, // sand
		{"#E2ECF6", "#CBDDEF"}, // mist
		{"#F7E4E2", "#EFCFCB"}, // blush
		{"#E9E8E4", "#D8D6CF"}, // stone
		{"#F6F0D8", "#ECE0B2"}, // butter
		{"#DFF1EE", "#C3E5DF"}, // mint
	}
	// Skin {base, shade}.
	skinTones = []pair{
		{"#FDE7D8", "#F4CDB6"}, // fair
		{"#FBDCC4", "#EFC2A3"},
		{"#F2C8A5", "#E3AE87"},
		{"#E3AF85", "#CF9468"},
		{"#C98D61", "#B07549"},
		{"#A56A44", "#8C5636"},
		{"#7A4A2F", "#653B24"},
	}
	// Hair {base, shade}.
	hairTones = []pair{
		{"#3B2B22", "#2A1E18"}, // espresso
		{"#5E3B24", "#47291A"}, // chestnut
		{"#8A6A3E", "#6E522D"}, // walnut
		{"#D9B36A", "#C29A4E"}, // honey
		{"#9A9A9E", "#808085"}, // silver
		{"#1F2024", "#141518"}, // ink
		{"#A9502F", "#8C3E22"}, // copper
		{"#E8E2D6", "#D6CEBE"}, // platinum
	}
	// Clothing {base, collar shade}: deep, muted, never neon.
	shirtColors = []pair{
		{"#2E3A59", "#253049"}, // navy
		{"#2F5D50", "#264C41"}, // forest
		{"#B5573A", "#984830"}, // terracotta
		{"#5B3A63", "#4B2F52"}, // plum
		{"#C98E2B", "#AB7823"}, // ochre
		{"#4A5568", "#3D4656"}, // slate
		{"#1F6F78", "#195C63"}, // teal
		{"#7A2E3A", "#652530"}, // burgundy
	}
	// Scarf and knit colours.
	accentColors = []string{"#C2533F", "#3D5A80", "#5E7D4F", "#8E5572", "#B8862F"}
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

// Hairstyles. hairBack draws behind the head, hairFront over the forehead.
const (
	hairCrop = iota
	hairSidePart
	hairLong
	hairBun
	hairCurls
	hairBeanie
	hairBob
	hairBuzz
	hairPonytail
	hairScarf
	hairBald
	hairQuiff
)

// hairIndexFor chooses a default hairstyle from gender when the caller did not pin
// a style. The beanie and the headscarf are worn by choice, so they are only ever
// picked, never assigned.
func hairIndexFor(gender string, h uint32) int {
	switch strings.ToLower(strings.TrimSpace(gender)) {
	case "female", "f", "woman":
		return []int{hairLong, hairBob, hairBun, hairPonytail, hairCurls}[int(h)%5]
	case "male", "m", "man":
		return []int{hairCrop, hairSidePart, hairQuiff, hairBuzz}[int(h)%4]
	default:
		return []int{hairCrop, hairSidePart, hairLong, hairCurls, hairBob, hairQuiff}[int(h)%6]
	}
}

// look is one face's choices.
type look struct {
	hair, mouth, skin     int
	glasses, beard, rings bool
	vneck                 bool
}

// cartoons are the prebuilt looks the picker offers, each distinct at 40 px.
//
// Each carries its own skin tone, so the set offers the whole range whoever is
// choosing: the tone came from the address, and an address that drew a dark
// tone could not pick a fair face, nor the reverse (3cp). Tones index skinTones.
var cartoons = [...]look{
	{hair: hairCrop, mouth: 0, skin: 0},
	{hair: hairSidePart, mouth: 1, skin: 3, glasses: true},
	{hair: hairLong, mouth: 0, skin: 0, rings: true},
	{hair: hairBun, mouth: 2, skin: 1, vneck: true},
	{hair: hairCurls, mouth: 0, skin: 5},
	{hair: hairBuzz, mouth: 1, skin: 2, beard: true, vneck: true},
	{hair: hairBeanie, mouth: 2, skin: 0},
	{hair: hairBob, mouth: 1, skin: 1, glasses: true},
	{hair: hairPonytail, mouth: 2, skin: 4, rings: true},
	{hair: hairScarf, mouth: 0, skin: 2},
	{hair: hairBald, mouth: 1, skin: 6, beard: true, glasses: true},
	{hair: hairQuiff, mouth: 2, skin: 0, vneck: true},
}

// Face renders a cartoon avatar SVG for the seed. style >= 0 pins a prebuilt
// cartoon; otherwise the face is derived from the seed and gender.
func Face(seed string, style int, gender string) string {
	w := seedWords(seed)

	// A short id suffix keeps the gradient id unique if two avatars are ever
	// inlined into the same document (normally each is its own <img>).
	uid := strconv.FormatUint(uint64(w[7])^(uint64(style+1)*0x9E3779B1)&0xffffffff, 16)
	if len(uid) > 8 {
		uid = uid[len(uid)-8:]
	}

	bg := pick(bgPairs, w[0])
	skin := pick(skinTones, w[1])
	hair := pick(hairTones, w[2])
	shirt := pick(shirtColors, w[5])
	accent := pick(accentColors, w[6])

	var lk look
	if style >= 0 {
		lk = cartoons[style%len(cartoons)]
		skin = skinTones[lk.skin%len(skinTones)]
		// Background and hair colour still vary by seed, so two people who pick
		// the same cartoon differ.
		bg = pick(bgPairs, w[0]+uint32(style))
	} else {
		lk = look{
			hair:    hairIndexFor(gender, w[3]),
			mouth:   int(w[4]) % len(mouthPaths),
			glasses: w[5]%5 == 0,
			beard:   strings.HasPrefix(strings.ToLower(strings.TrimSpace(gender)), "m") && w[6]%3 == 0,
			vneck:   w[4]%2 == 0,
		}
	}

	var b strings.Builder
	b.Grow(3200)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="8 6 84 84" width="100" height="100" role="img" aria-label="avatar">`)

	// One internal paint server: the light falling on the face from the upper
	// left. Internal fragment refs only, so it is safe under a strict CSP.
	b.WriteString(`<defs><radialGradient id="lt` + uid + `" cx="0.35" cy="0.3" r="0.75">` +
		`<stop offset="0" stop-color="#ffffff" stop-opacity="0.22"/><stop offset="1" stop-color="#ffffff" stop-opacity="0"/></radialGradient></defs>`)

	// ── Ground: a flat tint and a soft disc that frames the head ────────────────
	b.WriteString(`<rect width="100" height="100" fill="` + bg[0] + `"/>`)
	b.WriteString(`<circle cx="50" cy="46" r="37" fill="` + bg[1] + `"/>`)

	// ── Hair that falls behind the head and shoulders ───────────────────────────
	b.WriteString(hairBack(lk.hair, hair, accent))

	// ── Shoulders, collar and neck ───────────────────────────────────────────────
	b.WriteString(`<path d="M12 100c0-14.5 12.5-24.5 29.5-26.5h17c17 2 29.5 12 29.5 26.5z" fill="` + shirt[0] + `"/>`)
	b.WriteString(`<path d="M43.5 60h13v12.5c-4.3 3.4-8.7 3.4-13 0z" fill="` + skin[1] + `"/>`)
	if lk.vneck {
		b.WriteString(`<path d="M41.5 73.5 50 85l8.5-11.5c-2.2-.5-4.6-.8-6.9-.9L50 75l-1.6-2.4c-2.3.1-4.7.4-6.9.9z" fill="` + skin[1] + `"/>`)
		b.WriteString(`<path d="M41.5 73.5 50 85l8.5-11.5" fill="none" stroke="` + shirt[1] + `" stroke-width="2.4" stroke-linejoin="round"/>`)
	} else {
		b.WriteString(`<path d="M41.8 73.6c2.2 3.6 5 5.2 8.2 5.2s6-1.6 8.2-5.2" fill="none" stroke="` + shirt[1] + `" stroke-width="2.6" stroke-linecap="round"/>`)
	}
	if lk.hair == hairScarf {
		b.WriteString(`<path d="M30.5 52c2 14 9.5 22.5 19.5 22.5S67.5 66 69.5 52c3 9.5 2.5 19-4 24.5-4.2 3.6-9.4 5-15.5 5s-11.3-1.4-15.5-5c-6.5-5.5-7-15-4-24.5z" fill="` + accent + `"/>`)
	}

	// ── Ears and head ────────────────────────────────────────────────────────────
	if lk.hair != hairScarf {
		b.WriteString(`<ellipse cx="31.2" cy="47.5" rx="3.6" ry="5" fill="` + skin[1] + `"/><ellipse cx="68.8" cy="47.5" rx="3.6" ry="5" fill="` + skin[1] + `"/>`)
	}
	// A round, soft face: wide at the cheeks, a short chin.
	face := `M31 45C31 31 39.4 21.5 50 21.5S69 31 69 45c0 13.2-8.5 23-19 23S31 58.2 31 45z`
	b.WriteString(`<path d="` + face + `" fill="` + skin[0] + `"/>`)
	b.WriteString(`<path d="` + face + `" fill="url(#lt` + uid + `)"/>`)
	// The jaw's shadow on the neck, so the head sits on it rather than on top.
	b.WriteString(`<path d="M41 63.5c2.6 3.2 5.6 5.3 9 5.3s6.4-2.1 9-5.3c-2.4 1.4-5.6 2.2-9 2.2s-6.6-.8-9-2.2z" fill="` + skin[1] + `" opacity="0.55"/>`)
	if lk.rings {
		b.WriteString(`<circle cx="31.4" cy="54" r="1.5" fill="none" stroke="#D9B45A" stroke-width="1.1"/><circle cx="68.6" cy="54" r="1.5" fill="none" stroke="#D9B45A" stroke-width="1.1"/>`)
	}

	// ── Beard, before the mouth so the mouth reads on top ───────────────────────
	if lk.beard {
		b.WriteString(`<path d="M32.5 50c.6 11.5 8 19 17.5 19s16.9-7.5 17.5-19c-1.6 4.3-4.5 6.9-8.4 7.6-2.5.5-5.3-1.6-9.1-1.6s-6.6 2.1-9.1 1.6c-3.9-.7-6.8-3.3-8.4-7.6z" fill="` + hair[0] + `"/>`)
	}

	// ── Hair over the forehead ───────────────────────────────────────────────────
	b.WriteString(hairFront(lk.hair, hair, accent))

	// ── Brows, eyes, nose, cheeks ────────────────────────────────────────────────
	brow := hair[1]
	if lk.hair == hairBald || lk.hair == hairScarf || lk.hair == hairBeanie {
		brow = "#3B2B22"
	}
	b.WriteString(`<path d="M38.2 40.4c2.3-1.7 5.2-2 7.6-.9M54.2 39.5c2.4-1.1 5.3-.8 7.6.9" fill="none" stroke="` + brow + `" stroke-width="2.1" stroke-linecap="round"/>`)
	b.WriteString(`<ellipse cx="42.2" cy="46.6" rx="2.6" ry="3.2" fill="#2A211C"/><ellipse cx="57.8" cy="46.6" rx="2.6" ry="3.2" fill="#2A211C"/>`)
	b.WriteString(`<circle cx="43.2" cy="45.3" r="1" fill="#ffffff"/><circle cx="58.8" cy="45.3" r="1" fill="#ffffff"/>`)
	b.WriteString(`<circle cx="41.4" cy="48" r=".45" fill="#ffffff" opacity="0.8"/><circle cx="57" cy="48" r=".45" fill="#ffffff" opacity="0.8"/>`)
	b.WriteString(`<path d="M48.4 53.1c1 .6 2.2.6 3.2 0" fill="none" stroke="` + skin[1] + `" stroke-width="1.5" stroke-linecap="round"/>`)
	b.WriteString(`<ellipse cx="37.4" cy="53.4" rx="3.6" ry="2.2" fill="#F2858F" opacity="0.32"/><ellipse cx="62.6" cy="53.4" rx="3.6" ry="2.2" fill="#F2858F" opacity="0.32"/>`)

	// ── Mouth ────────────────────────────────────────────────────────────────────
	b.WriteString(mouthPaths[lk.mouth%len(mouthPaths)])

	// ── Glasses: thin round frames ───────────────────────────────────────────────
	if lk.glasses {
		b.WriteString(`<g fill="#ffffff" fill-opacity="0.12" stroke="#2B2F36" stroke-width="1.5"><circle cx="42.2" cy="46.6" r="5.6"/><circle cx="57.8" cy="46.6" r="5.6"/></g>`)
		b.WriteString(`<path d="M47.8 46.2c1.4-.9 3-.9 4.4 0M36.6 45.6l-4.6-1.4M63.4 45.6l4.6-1.4" fill="none" stroke="#2B2F36" stroke-width="1.5" stroke-linecap="round"/>`)
	}

	b.WriteString(`</svg>`)
	return b.String()
}

// mouthPaths: a soft smile, a gentle one, and an open smile.
var mouthPaths = []string{
	`<path d="M45.4 58c2.7 2.5 6.5 2.5 9.2 0" fill="none" stroke="#8A4A3D" stroke-width="1.9" stroke-linecap="round"/>`,
	`<path d="M46.6 58.8c2.1 1.2 4.7 1.2 6.8 0" fill="none" stroke="#8A4A3D" stroke-width="1.8" stroke-linecap="round"/>`,
	`<path d="M45 57.4h10c0 3.4-2.2 5.4-5 5.4s-5-2-5-5.4z" fill="#7A3530"/><path d="M45.7 57.4h8.6c-.2.9-.5 1.5-.9 1.8h-6.8c-.4-.3-.7-.9-.9-1.8z" fill="#ffffff"/><path d="M47.3 61.6c1.6.9 3.8.9 5.4 0-1.4-.8-4-.8-5.4 0z" fill="#D9786E"/>`,
}

// hairBack returns the hair drawn behind the head, or "" for styles that sit
// entirely on top.
func hairBack(idx int, hair pair, accent string) string {
	h, s := hair[0], hair[1]
	switch idx {
	case hairLong:
		return `<path d="M29.5 44C29.5 27 38.5 17.5 50 17.5S70.5 27 70.5 44v27c0 4-3 6.5-6.5 6.5h-28c-3.5 0-6.5-2.5-6.5-6.5z" fill="` + s + `"/>`
	case hairBob:
		return `<path d="M28.5 44.5C28.5 27.5 38 18 50 18s21.5 9.5 21.5 26.5v14.5c0 3-2.2 5-5 5H33.5c-2.8 0-5-2-5-5z" fill="` + s + `"/>`
	case hairCurls:
		return `<g fill="` + s + `"><circle cx="50" cy="25" r="17"/><circle cx="34" cy="30" r="11"/><circle cx="66" cy="30" r="11"/><circle cx="29" cy="43" r="9"/><circle cx="71" cy="43" r="9"/><circle cx="30.5" cy="55" r="7"/><circle cx="69.5" cy="55" r="7"/></g>` +
			`<g fill="` + h + `"><circle cx="42" cy="19.5" r="7"/><circle cx="56" cy="18.5" r="7.5"/><circle cx="33" cy="28" r="6"/><circle cx="67" cy="28" r="6"/></g>`
	case hairPonytail:
		return `<path d="M64 30c9 2 13 11 11.5 22-.8 6-4.5 10-8.5 11.5 2.5-6 2.8-13 .2-19.5-1.2-3.2-3-6-5.7-8z" fill="` + s + `"/>`
	case hairBun:
		return `<circle cx="50" cy="15.5" r="8.6" fill="` + s + `"/><circle cx="48" cy="13.5" r="3.4" fill="` + h + `" opacity="0.55"/>`
	case hairScarf:
		return `<path d="M27 46C27 26.5 37 16 50 16s23 10.5 23 30c0 12-3.5 21-9 26H36c-5.5-5-9-14-9-26z" fill="` + accent + `"/>`
	default:
		return ""
	}
}

// hairFront returns the hair, hat or scarf drawn over the forehead.
func hairFront(idx int, hair pair, accent string) string {
	h, s := hair[0], hair[1]
	cap := `<path d="M30.8 43.5C29.8 28.5 38.5 19 50 19s20.2 9.5 19.2 24.5c-1.7-6.4-5-10.6-9.6-12.9-3.2 2.3-7.4 3.4-11.6 3.3-6.6-.2-12.2 1.4-15 5.4-.9 1.3-1.6 2.7-2.2 4.2z" fill="` + h + `"/>`
	switch idx {
	case hairCrop:
		return cap + `<path d="M38 26.5c3.5-3 8-4.4 12.5-4.3M44 24.2c3-1.3 6.4-1.5 9.6-.6" fill="none" stroke="` + s + `" stroke-width="1.2" stroke-linecap="round" opacity="0.6"/>`
	case hairSidePart:
		return `<path d="M30.6 44.5C29 28 38 18.5 51 18.5c11.5 0 19.5 8.5 18.4 23.2-2-6.6-5.8-10.6-11-12.3-4.9 3.9-12.4 5.6-19.9 4.7-3.3 2.4-5.8 5.8-7.9 10.4z" fill="` + h + `"/>` +
			`<path d="M57.5 29.2c-3 3.6-8.4 6-14.6 6.5" fill="none" stroke="` + s + `" stroke-width="1.3" stroke-linecap="round" opacity="0.7"/>`
	case hairLong, hairBob:
		return `<path d="M31 46C29.6 29 38.5 19.3 50 19.3S70.4 29 69 46c-1.4-7.6-4.6-12.4-9.3-14.6-2.4 3-6.2 4.6-9.7 4.6s-7.3-1.6-9.7-4.6c-4.7 2.2-7.9 7-9.3 14.6z" fill="` + h + `"/>`
	case hairBun:
		return `<path d="M31 43.6C30.2 29 38.8 20 50 20s19.8 9 19 23.6c-2.2-7.8-9.3-12.3-19-12.3s-16.8 4.5-19 12.3z" fill="` + h + `"/>`
	case hairCurls:
		return `<g fill="` + h + `"><circle cx="38" cy="29.5" r="5.6"/><circle cx="46" cy="26.5" r="6"/><circle cx="54.5" cy="26.5" r="6"/><circle cx="62.2" cy="29.5" r="5.6"/></g>`
	case hairBeanie:
		return `<path d="M29.8 38.5C29.8 24 38.8 15 50 15s20.2 9 20.2 23.5z" fill="` + accent + `"/>` +
			`<rect x="28" y="34" width="44" height="8.5" rx="4.25" fill="` + accent + `"/>` +
			`<rect x="28" y="34" width="44" height="8.5" rx="4.25" fill="#000000" fill-opacity="0.16"/>` +
			`<path d="M34 34v8.5M40 34v8.5M46 34v8.5M52 34v8.5M58 34v8.5M64 34v8.5" stroke="#000000" stroke-opacity="0.12" stroke-width="1.2"/>` +
			`<circle cx="50" cy="14.5" r="4.2" fill="` + accent + `"/>`
	case hairBuzz:
		return `<path d="M31.6 42.5C31.2 29 39.4 20.6 50 20.6S68.8 29 68.4 42.5c-2.6-6.6-9.4-10.6-18.4-10.6s-15.8 4-18.4 10.6z" fill="` + h + `" opacity="0.85"/>`
	case hairPonytail:
		return cap + `<path d="M62 25.5c3 1.2 5.2 3.4 6.4 6.5" fill="none" stroke="` + s + `" stroke-width="2.6" stroke-linecap="round"/>`
	case hairScarf:
		return `<path d="M30.4 46C29.4 28.8 38.4 19.2 50 19.2S70.6 28.8 69.6 46c-2.6-9.6-9.7-15.6-19.6-15.6S33 36.4 30.4 46z" fill="` + accent + `"/>` +
			`<path d="M33 41c3.8-6.8 9.8-10.4 17-10.4s13.2 3.6 17 10.4" fill="none" stroke="#000000" stroke-opacity="0.12" stroke-width="1.4" stroke-linecap="round"/>`
	case hairBald:
		return `<path d="M38 26.5c3.5-2.6 7.6-3.8 12-3.8" fill="none" stroke="#ffffff" stroke-opacity="0.35" stroke-width="2.2" stroke-linecap="round"/>`
	case hairQuiff:
		return `<path d="M30.8 43.5C29.6 28.6 37.6 18 50 16.5c7-.8 13.6 1.4 16.6 6.8 1.6 2.9 2.9 7.4 2.6 20.2-1.7-6.2-4.9-10.3-9.4-12.6-6.6 2.6-15 2.4-20.4.5-4.5 2.6-7.4 6.6-8.6 12.1z" fill="` + h + `"/>` +
			`<path d="M45 19.8c5-2.6 12-2.4 16.4 1.5" fill="none" stroke="` + s + `" stroke-width="1.4" stroke-linecap="round" opacity="0.7"/>`
	default:
		return cap
	}
}

// Auto renders the deterministic, gender-aware avatar for a seed (member email).
func Auto(seed, gender string) string { return Face(seed, -1, gender) }

// CartoonCount is how many prebuilt cartoons the picker offers.
const CartoonCount = len(cartoons)

// Cartoon renders prebuilt cartoon n (0..CartoonCount-1) for the seed. Colour
// still varies by seed so the same choice looks personal per member.
func Cartoon(n int, seed string) string {
	if n < 0 {
		n = 0
	}
	return Face(seed, n%CartoonCount, "")
}
