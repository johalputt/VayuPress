// SPDX-License-Identifier: Apache-2.0

package ui

// icons.go — the Still Air icon set.
//
// One set, drawn to one rule: a 20-unit grid, 1.5 stroke, round caps and joins,
// no fills. Every icon the console shows comes from this map; an icon from
// another library, or an emoji standing in for one, does not belong in it.

import (
	"sort"
	"strconv"
	"strings"
)

var icons = map[string]string{
	"archive":   `<rect x="2.9" y="3.8" width="14.2" height="3.6" rx=".8"/><path d="M4.1 7.4v8a.8.8 0 0 0 .8.8h10.2a.8.8 0 0 0 .8-.8v-8M8.2 10.4h3.6"/>`,
	"arrow-r":   `<path d="M4 10h11.4M11 5.6l4.4 4.4-4.4 4.4"/>`,
	"audience":  `<circle cx="7.6" cy="7" r="2.8"/><path d="M2.8 16.2c.5-2.6 2.4-4.2 4.8-4.2s4.3 1.6 4.8 4.2"/><path d="M13 4.5a2.6 2.6 0 0 1 0 5M14.6 12.3c1.4.5 2.3 1.8 2.6 3.9"/>`,
	"bell":      `<path d="M5.2 13.8V9a4.8 4.8 0 0 1 9.6 0v4.8l1.2 1.4H4z"/><path d="M8.4 17.2a1.8 1.8 0 0 0 3.2 0"/>`,
	"bolt":      `<path d="M11.2 2.8 4.8 11.2h4.8l-.8 6 6.4-8.4h-4.8z"/>`,
	"book":      `<path d="M4.4 4.2a1.4 1.4 0 0 1 1.4-1.4h9.8v11.4H5.8a1.4 1.4 0 0 0-1.4 1.4zM4.4 15.6a1.4 1.4 0 0 0 1.4 1.4h9.8"/>`,
	"bot":       `<rect x="4" y="6.6" width="12" height="9" rx="2"/><path d="M10 6.6V4M8.4 3.6h3.2M2.6 10.4v2.4M17.4 10.4v2.4M7.6 11h.1M12.4 11h.1"/>`,
	"card":      `<rect x="2.8" y="4.6" width="14.4" height="10.8" rx="1.4"/><path d="M2.8 8.2h14.4M5.6 12.4h3"/>`,
	"cert":      `<rect x="3" y="3.6" width="14" height="10" rx="1.2"/><path d="M6.2 7.2h7.6M6.2 10h4"/><circle cx="13.2" cy="13.6" r="2"/><path d="m12.2 15.4-.6 2.2 1.6-.8 1.6.8-.6-2.2"/>`,
	"chart":     `<path d="M3.6 16.4h12.8M6 13.4V9.6M10 13.4V5.4M14 13.4v-6"/>`,
	"check":     `<path d="m4.5 10.3 3.6 3.6 7.4-7.6"/>`,
	"check-c":   `<circle cx="10" cy="10" r="7.2"/><path d="m6.9 10.2 2.2 2.2 4.1-4.4"/>`,
	"chev-d":    `<path d="m6 8 4 4 4-4"/>`,
	"chev-r":    `<path d="m8 6 4 4-4 4"/>`,
	"chev-ud":   `<path d="m7 8 3-3 3 3M7 12l3 3 3-3"/>`,
	"clip":      `<path d="m15.6 9.4-5.8 5.8a3.6 3.6 0 0 1-5.1-5.1l6.4-6.4a2.4 2.4 0 0 1 3.4 3.4L8.2 13.4a1.2 1.2 0 0 1-1.7-1.7l5.5-5.5"/>`,
	"cmd":       `<path d="M7 7h6v6H7zM7 7V5.4A1.6 1.6 0 1 0 5.4 7zm6 0V5.4A1.6 1.6 0 1 1 14.6 7zm0 6v1.6a1.6 1.6 0 1 0 1.6-1.6zm-6 0v1.6A1.6 1.6 0 1 1 5.4 13z"/>`,
	"coin":      `<circle cx="10" cy="10" r="7.2"/><path d="M12.2 7.4c-.4-.8-1.2-1.2-2.2-1.2-1.3 0-2.3.7-2.3 1.8 0 2.5 4.8 1.4 4.8 4 0 1.1-1 1.9-2.4 1.9-1.1 0-2-.5-2.4-1.3M10 4.8v1.4M10 13.9v1.3"/>`,
	"columns":   `<path d="M3 7.4 10 3.4l7 4M4.6 7.8v6.4M8.2 7.8v6.4M11.8 7.8v6.4M15.4 7.8v6.4M3 16.6h14"/>`,
	"compass":   `<circle cx="10" cy="10" r="7.2"/><path d="m12.8 7.2-1.6 4-4 1.6 1.6-4z"/>`,
	"content":   `<path d="M5.5 2.8h6l3.5 3.5v10.1a.8.8 0 0 1-.8.8H5.5a.8.8 0 0 1-.8-.8V3.6a.8.8 0 0 1 .8-.8z"/><path d="M11.3 2.9v3.6h3.6M7.4 10.2h5.2M7.4 13.2h3.6"/>`,
	"copy":      `<rect x="7" y="7" width="9.4" height="9.4" rx="1.2"/><path d="M13 7V4.4a.8.8 0 0 0-.8-.8H4.4a.8.8 0 0 0-.8.8v7.8a.8.8 0 0 0 .8.8H7"/>`,
	"db":        `<ellipse cx="10" cy="5" rx="6" ry="2.2"/><path d="M4 5v10c0 1.2 2.7 2.2 6 2.2s6-1 6-2.2V5M4 10c0 1.2 2.7 2.2 6 2.2s6-1 6-2.2"/>`,
	"disk":      `<rect x="2.8" y="5" width="14.4" height="10" rx="1.4"/><path d="M5.6 12.2h.1M8.4 12.2h6"/>`,
	"doc":       `<path d="M5.5 2.8h6l3.5 3.5v10.1a.8.8 0 0 1-.8.8H5.5a.8.8 0 0 1-.8-.8V3.6a.8.8 0 0 1 .8-.8z"/><path d="M11.3 2.9v3.6h3.6"/>`,
	"download":  `<path d="M10 3.6v9.2M6.4 9.2 10 12.8l3.6-3.6M3.8 12.6v2.8a.8.8 0 0 0 .8.8h10.8a.8.8 0 0 0 .8-.8v-2.8"/>`,
	"draft":     `<path d="M12.8 3.8 16.2 7.2 7.5 15.9H4.1v-3.4z"/>`,
	"error":     `<circle cx="10" cy="10" r="7.2"/><path d="m7.6 7.6 4.8 4.8M12.4 7.6l-4.8 4.8"/>`,
	"expand":    `<path d="M3.6 7.6v-4h4M12.4 3.6h4v4M16.4 12.4v4h-4M7.6 16.4h-4v-4"/>`,
	"ext":       `<path d="M11 3.8h5.2V9M16 4 9.4 10.6M14.2 12.2v3.2a.8.8 0 0 1-.8.8H4.6a.8.8 0 0 1-.8-.8V6.6a.8.8 0 0 1 .8-.8h3.2"/>`,
	"eye":       `<path d="M2.6 10S5.2 4.8 10 4.8s7.4 5.2 7.4 5.2-2.6 5.2-7.4 5.2S2.6 10 2.6 10z"/><circle cx="10" cy="10" r="2.3"/>`,
	"filter":    `<path d="M3.4 4.6h13.2M6 10h8M8.6 15.4h2.8"/>`,
	"flow":      `<rect x="2.8" y="3.4" width="5" height="4.2" rx="1"/><rect x="12.2" y="12.4" width="5" height="4.2" rx="1"/><path d="M5.3 7.6v3.2a1.6 1.6 0 001.6 1.6h5.3"/>`,
	"folder":    `<path d="M2.9 5.2a.8.8 0 0 1 .8-.8h4l1.6 1.8h7a.8.8 0 0 1 .8.8v8.6a.8.8 0 0 1-.8.8H3.7a.8.8 0 0 1-.8-.8z"/>`,
	"forward":   `<path d="m12 5 4.5 4.5L12 14"/><path d="M16.2 9.5H8.8a5 5 0 0 0-5 5v1"/>`,
	"globe":     `<circle cx="10" cy="10" r="7.2"/><path d="M2.8 10h14.4M10 2.8c2 2 3 4.4 3 7.2s-1 5.2-3 7.2c-2-2-3-4.4-3-7.2s1-5.2 3-7.2z"/>`,
	"grid":      `<rect x="3.4" y="3.4" width="5.4" height="5.4" rx=".8"/><rect x="11.2" y="3.4" width="5.4" height="5.4" rx=".8"/><rect x="3.4" y="11.2" width="5.4" height="5.4" rx=".8"/><rect x="11.2" y="11.2" width="5.4" height="5.4" rx=".8"/>`,
	"home":      `<path d="M3.5 9.2 10 3.8l6.5 5.4V16a.8.8 0 0 1-.8.8h-3.4v-4.6H7.7v4.6H4.3a.8.8 0 0 1-.8-.8z"/>`,
	"hourglass": `<path d="M5.6 3h8.8M5.6 17h8.8M6.4 3v2.4c0 1.6 1.2 2.8 3.6 4.6 2.4-1.8 3.6-3 3.6-4.6V3M6.4 17v-2.4c0-1.6 1.2-2.8 3.6-4.6 2.4 1.8 3.6 3 3.6 4.6V17"/>`,
	"idcard":    `<rect x="2.8" y="4.6" width="14.4" height="10.8" rx="1.4"/><circle cx="7.4" cy="9.2" r="1.6"/><path d="M4.8 13c.4-1.2 1.4-1.8 2.6-1.8s2.2.6 2.6 1.8M12 8.4h3M12 11h3"/>`,
	"image":     `<rect x="3" y="3.8" width="14" height="12.4" rx="1.4"/><circle cx="7.6" cy="8" r="1.4"/><path d="m3.4 14.6 4.4-4 3.2 2.8 2.2-1.8 3.4 3"/>`,
	"inbox":     `<path d="M3 11.2 5 4.6h10l2 6.6v4.2a.8.8 0 0 1-.8.8H3.8a.8.8 0 0 1-.8-.8z"/><path d="M3 11.2h4.2l1 1.8h3.6l1-1.8H17"/>`,
	"info":      `<circle cx="10" cy="10" r="7.2"/><path d="M10 9.2v4.4M10 6.6v.1"/>`,
	"key":       `<circle cx="6.8" cy="13.2" r="3.2"/><path d="m9.1 10.9 7.1-7.1M13.4 6.6l2 2M11.6 8.4l1.6 1.6"/>`,
	"keyboard":  `<rect x="2.6" y="5" width="14.8" height="10" rx="1.4"/><path d="M5.4 8h.1M8.4 8h.1M11.6 8h.1M14.6 8h.1M5.4 10.8h.1M14.6 10.8h.1M7.6 12.2h4.8"/>`,
	"link":      `<path d="M8.4 11.6a3 3 0 0 0 4.2 0l2.6-2.6a3 3 0 0 0-4.2-4.2l-.9.9M11.6 8.4a3 3 0 0 0-4.2 0L4.8 11a3 3 0 0 0 4.2 4.2l.9-.9"/>`,
	"list":      `<path d="M7.2 5.2h9.4M7.2 10h9.4M7.2 14.8h9.4M3.6 5.2h.1M3.6 10h.1M3.6 14.8h.1"/>`,
	"lock":      `<rect x="4.5" y="8.8" width="11" height="8" rx="1.4"/><path d="M7 8.8V6.6a3 3 0 0 1 6 0v2.2"/>`,
	"mail":      `<rect x="2.8" y="4.3" width="14.4" height="11.4" rx="1.6"/><path d="m3.4 5.4 6.6 5.3 6.6-5.3"/>`,
	"megaphone": `<path d="M3.5 8.2v3.6h2.6l6.4 3.6V4.6L6.1 8.2z"/><path d="M6.4 11.8l1 4h2.2l-.8-3.4M15 7.6a3 3 0 010 4.8"/>`,
	"monitor":   `<rect x="2.8" y="3.6" width="14.4" height="9.8" rx="1.2"/><path d="M7.4 16.6h5.2M10 13.4v3.2"/>`,
	"moon":      `<path d="M15.8 12.4A6.4 6.4 0 0 1 7.6 4.2a6.4 6.4 0 1 0 8.2 8.2z"/>`,
	"more":      `<circle cx="5" cy="10" r="1"/><circle cx="10" cy="10" r="1"/><circle cx="15" cy="10" r="1"/>`,
	"move":      `<path d="M2.9 5.2a.8.8 0 0 1 .8-.8h4l1.6 1.8h7a.8.8 0 0 1 .8.8v8.6a.8.8 0 0 1-.8.8H3.7a.8.8 0 0 1-.8-.8z"/><path d="M8 11.2h5M11 9.2l2 2-2 2"/>`,
	"package":   `<path d="M10 2.8 16.6 6.4v7.2L10 17.2 3.4 13.6V6.4z"/><path d="M3.4 6.4 10 10l6.6-3.6M10 10v7.2"/>`,
	"palette":   `<path d="M10 2.8a7.2 7.2 0 1 0 0 14.4c1 0 1.6-.7 1.6-1.5 0-.9-.8-1.3-.8-2.1 0-.9.7-1.5 1.6-1.5h1.8a3 3 0 0 0 3-3C17.2 5.6 14 2.8 10 2.8z"/><path d="M6.6 9h.1M9.4 6.2h.1M13 7h.1"/>`,
	"pencil":    `<path d="M12.8 3.8 16.2 7.2 7.5 15.9H4.1v-3.4z"/>`,
	"phone":     `<rect x="5.6" y="2.6" width="8.8" height="14.8" rx="1.6"/><path d="M9 14.8h2"/>`,
	"pin":       `<path d="M7.6 3h4.8M8.4 3v4.4l-2.8 3.2h8.8l-2.8-3.2V3M10 10.6V17"/>`,
	"play":      `<path d="M6.4 4.2v11.6l9.2-5.8z"/>`,
	"plug":      `<path d="M7.2 3v3.6M12.8 3v3.6M5.2 6.6h9.6v2.6a4.8 4.8 0 0 1-9.6 0zM10 14v3.2"/>`,
	"plus":      `<path d="M10 4.5v11M4.5 10h11"/>`,
	"power":     `<path d="M10 3v6.4"/><path d="M6.2 5.4a6 6 0 107.6 0"/>`,
	"print":     `<path d="M6 7V3.4h8V7"/><rect x="3" y="7" width="14" height="6.4" rx="1.2"/><path d="M6 11.4h8v5.2H6z"/>`,
	"pulse":     `<path d="M2.8 10.4h3l2-4.6 3.6 9 2-4.4h3.8"/>`,
	"receipt":   `<path d="M5 2.8h10v14.4l-1.7-1.2-1.6 1.2-1.7-1.2-1.7 1.2-1.6-1.2L5 17.2z"/><path d="M7.6 6.8h4.8M7.6 9.8h4.8M7.6 12.8h2.6"/>`,
	"refresh":   `<path d="M16.2 9.4A6.2 6.2 0 0 0 5 6.2M3.8 10.6A6.2 6.2 0 0 0 15 13.8"/><path d="M4.6 3.2v3.3h3.3M15.4 16.8v-3.3h-3.3"/>`,
	"rename":    `<path d="M4 15.8h12M12.4 3.8l2.8 2.8-7 7H5.4v-2.8z"/>`,
	"reply":     `<path d="M8 5 3.5 9.5 8 14"/><path d="M3.8 9.5h7.4a5 5 0 0 1 5 5v1"/>`,
	"search":    `<circle cx="8.8" cy="8.8" r="5.3"/><path d="m12.8 12.8 4 4"/>`,
	"send":      `<path d="M16.8 3.2 8.6 11.4M16.8 3.2l-5.1 13.6-3.1-5.4-5.4-3.1z"/>`,
	"settings":  `<circle cx="10" cy="10" r="2.4"/><path d="M10 2.8v2M10 15.2v2M2.8 10h2M15.2 10h2M4.9 4.9l1.4 1.4M13.7 13.7l1.4 1.4M15.1 4.9l-1.4 1.4M6.3 13.7l-1.4 1.4"/>`,
	"shield":    `<path d="M10 2.6 4.2 4.8v4.6c0 3.6 2.4 6.3 5.8 8 3.4-1.7 5.8-4.4 5.8-8V4.8z"/>`,
	"site":      `<circle cx="10" cy="10" r="7.2"/><path d="M2.8 10h14.4M10 2.8c2 2 3 4.4 3 7.2s-1 5.2-3 7.2c-2-2-3-4.4-3-7.2s1-5.2 3-7.2z"/>`,
	"spam":      `<path d="M7 3h6l4 4v6l-4 4H7l-4-4V7z"/><path d="M10 6.8v3.8M10 13.2v.1"/>`,
	"sparkle":   `<path d="M10 3.2c.5 3.3 1.5 4.3 4.8 4.8-3.3.5-4.3 1.5-4.8 4.8-.5-3.3-1.5-4.3-4.8-4.8 3.3-.5 4.3-1.5 4.8-4.8zM15.2 12.6c.2 1.4.7 1.9 2 2.1-1.3.2-1.8.7-2 2.1-.2-1.4-.7-1.9-2-2.1 1.3-.2 1.8-.7 2-2.1z"/>`,
	"star":      `<path d="m10 3.2 2 4.3 4.6.5-3.4 3.1 1 4.6-4.2-2.4-4.2 2.4 1-4.6L3.4 8l4.6-.5z"/>`,
	"sun":       `<circle cx="10" cy="10" r="3.2"/><path d="M10 2.6v1.6M10 15.8v1.6M2.6 10h1.6M15.8 10h1.6M4.8 4.8l1.1 1.1M14.1 14.1l1.1 1.1M15.2 4.8l-1.1 1.1M5.9 14.1l-1.1 1.1"/>`,
	"swap":      `<path d="M3.6 7.4h12.8M13.4 4.4l3 3-3 3M16.4 12.6H3.6M6.6 9.6l-3 3 3 3"/>`,
	"system":    `<path d="M4 5.5h7M14.5 5.5H16M4 10h2M9.5 10H16M4 14.5h7M14.5 14.5H16"/><circle cx="12.8" cy="5.5" r="1.7"/><circle cx="7.8" cy="10" r="1.7"/><circle cx="12.8" cy="14.5" r="1.7"/>`,
	"tag":       `<path d="M3 3.4h6.4l7.6 7.6-6 6-7.6-7.6z"/><path d="M6.6 7h.1"/>`,
	"tablet":    `<rect x="4" y="2.6" width="12" height="14.8" rx="1.6"/><path d="M9 14.8h2"/>`,
	"talk":      `<path d="M4 4h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H9.5L6 16.8V14H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z"/>`,
	"target":    `<circle cx="10" cy="10" r="7.2"/><circle cx="10" cy="10" r="3.6"/><path d="M10 10h.1"/>`,
	"timer":     `<circle cx="10" cy="11" r="6"/><path d="M10 8v3.2l2 1.4M8 2.8h4"/>`,
	"topology":  `<circle cx="10" cy="4.6" r="1.9"/><circle cx="4.6" cy="15.2" r="1.9"/><circle cx="15.4" cy="15.2" r="1.9"/><path d="M8.9 6.2 5.6 13.5M11.1 6.2l3.3 7.3M6.5 15.2h7"/>`,
	"tor":       `<circle cx="10" cy="10" r="7.2"/><circle cx="10" cy="10" r="4.4"/><circle cx="10" cy="10" r="1.6"/>`,
	"trash":     `<path d="M3.8 5.6h12.4M8 5.6V3.9h4v1.7M5.3 5.6l.8 10.6a.8.8 0 0 0 .8.8h6.2a.8.8 0 0 0 .8-.8l.8-10.6"/>`,
	"trend":     `<path d="M3.4 14.6 7.6 10l3 3 6-6.6M12.4 6.4h4.2v4.2"/>`,
	"upload":    `<path d="M10 13V3.8M6.4 7.2 10 3.6l3.6 3.6M3.8 12.6v2.8a.8.8 0 0 0 .8.8h10.8a.8.8 0 0 0 .8-.8v-2.8"/>`,
	"user":      `<circle cx="10" cy="7" r="3.2"/><path d="M4 16.6c.6-3 3-4.8 6-4.8s5.4 1.8 6 4.8"/>`,
	"video":     `<rect x="2.8" y="4.8" width="10.4" height="10.4" rx="1.4"/><path d="m13.2 8.6 4-2.2v7.2l-4-2.2"/>`,
	"wall":      `<rect x="3" y="4" width="14" height="12" rx="1"/><path d="M3 8h14M3 12h14M8 4v4M12 8v4M8 12v4"/>`,
	"warn":      `<path d="M10 3.3 2.9 15.8h14.2z"/><path d="M10 8.3v3.3M10 13.9v.1"/>`,
	"where":     `<circle cx="9" cy="9" r="5.4"/><path d="m13 13 3.6 3.6M9 6.8v4.4M6.8 9h4.4"/>`,
	"wrench":    `<path d="M13.6 3.2a3.8 3.8 0 0 0-4.4 5L3.6 13.8a1.7 1.7 0 0 0 2.4 2.4l5.6-5.6a3.8 3.8 0 0 0 5-4.4l-2.3 2.3-2.1-.5-.5-2.1z"/>`,
	"x":         `<path d="m5.5 5.5 9 9M14.5 5.5l-9 9"/>`,
}

// Icon renders one icon from the set. An unknown name is a programming error
// that must not reach a page silently, so it renders the "missing" mark the
// chrome test looks for.
func Icon(name string) HTML {
	p, ok := icons[name]
	if !ok {
		return `<svg class="sa-ico sa-ico--missing" viewBox="0 0 20 20" aria-hidden="true"></svg>`
	}
	return HTML(`<svg class="sa-ico" viewBox="0 0 20 20" aria-hidden="true">` + p + `</svg>`)
}

// IconSized draws an icon that styles itself, for a page that carries no
// console stylesheet (the maintenance page, the offline page, the connector
// consent screen): the look Icon gets from CSS is spelled out as attributes.
func IconSized(name string, px int) HTML {
	sz := strconv.Itoa(px)
	return HTML(`<svg width="` + sz + `" height="` + sz + `" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + icons[name] + `</svg>`)
}

// IconNames lists the set, sorted.
func IconNames() []string {
	names := make([]string, 0, len(icons))
	for n := range icons {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Sprite carries the whole set once per page, so script-built markup draws the
// same icons as the server (vpIcon in admin-os.js) instead of an emoji.
var Sprite = func() HTML {
	var b strings.Builder
	b.WriteString(`<svg class="sa-sprite" width="0" height="0" aria-hidden="true" focusable="false"><defs>`)
	for _, n := range IconNames() {
		b.WriteString(`<symbol id="sa-i-` + n + `" viewBox="0 0 20 20">` + icons[n] + `</symbol>`)
	}
	b.WriteString(`</defs></svg>`)
	return HTML(b.String())
}()
