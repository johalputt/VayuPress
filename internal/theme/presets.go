package theme

import _ "embed"

		//go:embed gale.css
var galeCSS string

//go:embed zephyr.css
var zephyrCSS string

// Default returns the Default preset — neutral dark/light tones.
func Default() Tokens {
	return Tokens{
		Name:         "Default",
		BgDark:       "#0a0f1a",
		SurfaceDark:  "#111827",
		TextDark:     "#e5e7eb",
		MutedDark:    "#6b7280",
		AccentDark:   "#2dd4bf",
		Accent2Dark:  "#f59e0b",
		HiDark:       "#fbbf24",
		GreenDark:    "#34d399",
		BgLight:      "#f8fafc",
		SurfaceLight: "#ffffff",
		TextLight:    "#111827",
		MutedLight:   "#6b7280",
		AccentLight:  "#0d9488",
		Accent2Light: "#d97706",
		HiLight:      "#b45309",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.6",
		MaxWidth:     "72ch",
		RadiusSm:     "0.25rem",
		RadiusLg:     "0.75rem",
	}
}

// Aurora returns an aurora-inspired purple/teal preset.
func Aurora() Tokens {
	return Tokens{
		Name:         "Aurora",
		BgDark:       "#0d0b1e",
		SurfaceDark:  "#1a1533",
		TextDark:     "#e8e4f8",
		MutedDark:    "#7c6fa0",
		AccentDark:   "#a78bfa",
		Accent2Dark:  "#34d399",
		HiDark:       "#c4b5fd",
		GreenDark:    "#6ee7b7",
		BgLight:      "#f5f3ff",
		SurfaceLight: "#ffffff",
		TextLight:    "#1e1b4b",
		MutedLight:   "#7c6fa0",
		AccentLight:  "#7c3aed",
		Accent2Light: "#059669",
		HiLight:      "#5b21b6",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.65",
		MaxWidth:     "70ch",
		RadiusSm:     "0.375rem",
		RadiusLg:     "1rem",
	}
}

// Slate returns a cool-grey minimal preset.
func Slate() Tokens {
	return Tokens{
		Name:         "Slate",
		BgDark:       "#0f172a",
		SurfaceDark:  "#1e293b",
		TextDark:     "#cbd5e1",
		MutedDark:    "#64748b",
		AccentDark:   "#38bdf8",
		Accent2Dark:  "#fb923c",
		HiDark:       "#7dd3fc",
		GreenDark:    "#4ade80",
		BgLight:      "#f1f5f9",
		SurfaceLight: "#ffffff",
		TextLight:    "#0f172a",
		MutedLight:   "#64748b",
		AccentLight:  "#0284c7",
		Accent2Light: "#ea580c",
		HiLight:      "#0369a1",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.6",
		MaxWidth:     "72ch",
		RadiusSm:     "0.25rem",
		RadiusLg:     "0.5rem",
	}
}

// Terminal returns a green-on-black terminal/hacker preset.
func Terminal() Tokens {
	return Tokens{
		Name:         "Terminal",
		BgDark:       "#000000",
		SurfaceDark:  "#0d1117",
		TextDark:     "#00ff41",
		MutedDark:    "#005f1a",
		AccentDark:   "#00e676",
		Accent2Dark:  "#76ff03",
		HiDark:       "#b9f6ca",
		GreenDark:    "#69ff47",
		BgLight:      "#f0fff4",
		SurfaceLight: "#ffffff",
		TextLight:    "#003300",
		MutedLight:   "#2d6a4f",
		AccentLight:  "#00695c",
		Accent2Light: "#33691e",
		HiLight:      "#004d40",
		FontSans:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "0.9375rem",
		LineHeight:   "1.7",
		MaxWidth:     "80ch",
		RadiusSm:     "0",
		RadiusLg:     "0",
	}
}

// Sepia returns a warm parchment/sepia reading preset.
func Sepia() Tokens {
	return Tokens{
		Name:         "Sepia",
		BgDark:       "#1c1410",
		SurfaceDark:  "#2c2218",
		TextDark:     "#e8d5b7",
		MutedDark:    "#8b7355",
		AccentDark:   "#d4a96a",
		Accent2Dark:  "#c17f4e",
		HiDark:       "#f0c080",
		GreenDark:    "#8fbc8f",
		BgLight:      "#fdf6e3",
		SurfaceLight: "#fffef8",
		TextLight:    "#3d2b1f",
		MutedLight:   "#8b7355",
		AccentLight:  "#8b4513",
		Accent2Light: "#a0522d",
		HiLight:      "#6b3a2a",
		FontSans:     "Georgia,'Times New Roman',Times,serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1.0625rem",
		LineHeight:   "1.8",
		MaxWidth:     "68ch",
		RadiusSm:     "0.125rem",
		RadiusLg:     "0.25rem",
	}
}

// Carbon returns a high-contrast dark carbon preset.
func Carbon() Tokens {
	return Tokens{
		Name:         "Carbon",
		BgDark:       "#161616",
		SurfaceDark:  "#262626",
		TextDark:     "#f4f4f4",
		MutedDark:    "#8d8d8d",
		AccentDark:   "#4589ff",
		Accent2Dark:  "#ff7eb6",
		HiDark:       "#78a9ff",
		GreenDark:    "#42be65",
		BgLight:      "#f4f4f4",
		SurfaceLight: "#ffffff",
		TextLight:    "#161616",
		MutedLight:   "#6f6f6f",
		AccentLight:  "#0043ce",
		Accent2Light: "#9f1853",
		HiLight:      "#002d9c",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.6",
		MaxWidth:     "72ch",
		RadiusSm:     "0",
		RadiusLg:     "0.25rem",
	}
}

// Ocean returns a deep-ocean blue/teal preset.
func Ocean() Tokens {
	return Tokens{
		Name:         "Ocean",
		BgDark:       "#050d1a",
		SurfaceDark:  "#0a1628",
		TextDark:     "#cce4f7",
		MutedDark:    "#4a7fa5",
		AccentDark:   "#22d3ee",
		Accent2Dark:  "#67e8f9",
		HiDark:       "#a5f3fc",
		GreenDark:    "#2dd4bf",
		BgLight:      "#ecfeff",
		SurfaceLight: "#ffffff",
		TextLight:    "#083344",
		MutedLight:   "#4a7fa5",
		AccentLight:  "#0891b2",
		Accent2Light: "#0284c7",
		HiLight:      "#0e7490",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.65",
		MaxWidth:     "72ch",
		RadiusSm:     "0.375rem",
		RadiusLg:     "0.875rem",
	}
}

// Sakura returns a soft pink/cherry-blossom preset.
func Sakura() Tokens {
	return Tokens{
		Name:         "Sakura",
		BgDark:       "#1a0d10",
		SurfaceDark:  "#2d1520",
		TextDark:     "#fce7f3",
		MutedDark:    "#9d6878",
		AccentDark:   "#f472b6",
		Accent2Dark:  "#fb7185",
		HiDark:       "#fbcfe8",
		GreenDark:    "#86efac",
		BgLight:      "#fff0f6",
		SurfaceLight: "#ffffff",
		TextLight:    "#4a0d2a",
		MutedLight:   "#9d6878",
		AccentLight:  "#be185d",
		Accent2Light: "#e11d48",
		HiLight:      "#9d174d",
		FontSans:     "system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif",
		FontMono:     "ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono',monospace",
		FontSizeBase: "1rem",
		LineHeight:   "1.65",
		MaxWidth:     "70ch",
		RadiusSm:     "0.5rem",
		RadiusLg:     "1.25rem",
	}
}

// AllPresets returns a slice of all built-in presets in display order.
func AllPresets() []Tokens {
	return []Tokens{
		Default(),
		Aurora(),
		Slate(),
		Terminal(),
		Sepia(),
		Carbon(),
		Ocean(),
		Sakura(),
		Midnight(),
		Bloom(),
		Mint(),
		Solar(),
		Plum(),
		Fog(),
		Amber(),
		Pine(),
		Lavender(),
		Noir(),
		Meadow(),
		Rust(),
		Glacier(),
		Coral(),
		Gale(),
		Zephyr(),
	}
}

// Midnight — deep indigo, journal feel.
func Midnight() Tokens {
	return Tokens{
		Name: "Midnight", BgDark: "#0a0e1a", SurfaceDark: "#111827", TextDark: "#e2e8f0",
		MutedDark: "#64748b", AccentDark: "#818cf8", Accent2Dark: "#c4b5fd", HiDark: "#38bdf8", GreenDark: "#34d399",
		BgLight: "#f0f4ff", SurfaceLight: "#ffffff", TextLight: "#1e1b4b", MutedLight: "#6366f1",
		AccentLight: "#4f46e5", Accent2Light: "#7c3aed", HiLight: "#0ea5e9",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.75", MaxWidth: "44rem", RadiusSm: "0.375rem", RadiusLg: "0.5rem",
	}
}

// Bloom — warm rose, soft and welcoming.
func Bloom() Tokens {
	return Tokens{
		Name: "Bloom", BgDark: "#1a1015", SurfaceDark: "#2d1f2a", TextDark: "#fce7f3",
		MutedDark: "#9d4a70", AccentDark: "#f472b6", Accent2Dark: "#fda4af", HiDark: "#fb923c", GreenDark: "#86efac",
		BgLight: "#fff5f7", SurfaceLight: "#ffffff", TextLight: "#4a1526", MutedLight: "#be185d",
		AccentLight: "#db2777", Accent2Light: "#e11d48", HiLight: "#f97316",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.7", MaxWidth: "42rem", RadiusSm: "0.5rem", RadiusLg: "0.75rem",
	}
}

// Mint — fresh green, crisp and modern.
func Mint() Tokens {
	return Tokens{
		Name: "Mint", BgDark: "#0a1c14", SurfaceDark: "#143024", TextDark: "#d1fae5",
		MutedDark: "#6ee7b7", AccentDark: "#34d399", Accent2Dark: "#a7f3d0", HiDark: "#2dd4bf", GreenDark: "#4ade80",
		BgLight: "#f0fdf4", SurfaceLight: "#ffffff", TextLight: "#064e3b", MutedLight: "#059669",
		AccentLight: "#10b981", Accent2Light: "#047857", HiLight: "#0d9488",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1rem", LineHeight: "1.65", MaxWidth: "40rem", RadiusSm: "0.25rem", RadiusLg: "0.5rem",
	}
}

// Solar — warm amber-gold, energetic.
func Solar() Tokens {
	return Tokens{
		Name: "Solar", BgDark: "#1a1400", SurfaceDark: "#2d2500", TextDark: "#fef3c7",
		MutedDark: "#d97706", AccentDark: "#f59e0b", Accent2Dark: "#fbbf24", HiDark: "#f97316", GreenDark: "#10b981",
		BgLight: "#fffbeb", SurfaceLight: "#ffffff", TextLight: "#78350f", MutedLight: "#b45309",
		AccentLight: "#d97706", Accent2Light: "#ea580c", HiLight: "#f97316",
		FontSans: "system-ui, Inter, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.125rem", LineHeight: "1.8", MaxWidth: "48rem", RadiusSm: "0.5rem", RadiusLg: "0.75rem",
	}
}

// Plum — rich purple, sophisticated.
func Plum() Tokens {
	return Tokens{
		Name: "Plum", BgDark: "#120518", SurfaceDark: "#260a30", TextDark: "#f3e8ff",
		MutedDark: "#a855f7", AccentDark: "#c084fc", Accent2Dark: "#e9d5ff", HiDark: "#38bdf8", GreenDark: "#86efac",
		BgLight: "#faf5ff", SurfaceLight: "#ffffff", TextLight: "#3b0764", MutedLight: "#7e22ce",
		AccentLight: "#9333ea", Accent2Light: "#6b21a8", HiLight: "#0ea5e9",
		FontSans: "Inter, system-ui, serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.75", MaxWidth: "40rem", RadiusSm: "0.375rem", RadiusLg: "0.625rem",
	}
}

// Fog — misty grey, minimal and calm.
func Fog() Tokens {
	return Tokens{
		Name: "Fog", BgDark: "#1c1f26", SurfaceDark: "#2a2d37", TextDark: "#e5e7eb",
		MutedDark: "#9ca3af", AccentDark: "#9ca3af", Accent2Dark: "#d1d5db", HiDark: "#60a5fa", GreenDark: "#6ee7b7",
		BgLight: "#f3f4f6", SurfaceLight: "#ffffff", TextLight: "#1f2937", MutedLight: "#6b7280",
		AccentLight: "#6b7280", Accent2Light: "#4b5563", HiLight: "#3b82f6",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1rem", LineHeight: "1.6", MaxWidth: "40rem", RadiusSm: "0.25rem", RadiusLg: "0.375rem",
	}
}

// Amber — warm golden hour, retro film feel.
func Amber() Tokens {
	return Tokens{
		Name: "Amber", BgDark: "#1a1206", SurfaceDark: "#2d1f0a", TextDark: "#fef3c7",
		MutedDark: "#d97706", AccentDark: "#f59e0b", Accent2Dark: "#b45309", HiDark: "#fbbf24", GreenDark: "#a3e635",
		BgLight: "#fef9c3", SurfaceLight: "#fffef5", TextLight: "#5c3405", MutedLight: "#a16207",
		AccentLight: "#ca8a04", Accent2Light: "#854d0e", HiLight: "#eab308",
		FontSans: "Georgia, Inter, serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.125rem", LineHeight: "1.85", MaxWidth: "42rem", RadiusSm: "0", RadiusLg: "0.25rem",
	}
}

// Pine — forest green, grounded and natural.
func Pine() Tokens {
	return Tokens{
		Name: "Pine", BgDark: "#0a1a0f", SurfaceDark: "#14261a", TextDark: "#d1fae5",
		MutedDark: "#6ee7b7", AccentDark: "#34d399", Accent2Dark: "#059669", HiDark: "#fbbf24", GreenDark: "#4ade80",
		BgLight: "#f0fdf4", SurfaceLight: "#f7fee7", TextLight: "#14532d", MutedLight: "#15803d",
		AccentLight: "#16a34a", Accent2Light: "#166534", HiLight: "#eab308",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1rem", LineHeight: "1.65", MaxWidth: "44rem", RadiusSm: "0.25rem", RadiusLg: "0.375rem",
	}
}

// Lavender — soft purple, gentle and dreamy.
func Lavender() Tokens {
	return Tokens{
		Name: "Lavender", BgDark: "#160d24", SurfaceDark: "#281a3d", TextDark: "#ede9fe",
		MutedDark: "#a78bfa", AccentDark: "#c4b5fd", Accent2Dark: "#8b5cf6", HiDark: "#f472b6", GreenDark: "#a7f3d0",
		BgLight: "#f5f3ff", SurfaceLight: "#ffffff", TextLight: "#2e1065", MutedLight: "#7c3aed",
		AccentLight: "#8b5cf6", Accent2Light: "#6d28d9", HiLight: "#ec4899",
		FontSans: "system-ui, Inter, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.7", MaxWidth: "42rem", RadiusSm: "0.5rem", RadiusLg: "0.75rem",
	}
}

// Noir — true black, high contrast, editorial.
func Noir() Tokens {
	return Tokens{
		Name: "Noir", BgDark: "#000000", SurfaceDark: "#0a0a0a", TextDark: "#fafafa",
		MutedDark: "#737373", AccentDark: "#fafafa", Accent2Dark: "#a3a3a3", HiDark: "#e5e5e5", GreenDark: "#22c55e",
		BgLight: "#ffffff", SurfaceLight: "#fafafa", TextLight: "#0a0a0a", MutedLight: "#525252",
		AccentLight: "#171717", Accent2Light: "#404040", HiLight: "#404040",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.125rem", LineHeight: "1.8", MaxWidth: "48rem", RadiusSm: "0", RadiusLg: "0",
	}
}

// Meadow — soft green-yellow, fresh and organic.
func Meadow() Tokens {
	return Tokens{
		Name: "Meadow", BgDark: "#0f1a0a", SurfaceDark: "#1a2f12", TextDark: "#ecfccb",
		MutedDark: "#a3e635", AccentDark: "#84cc16", Accent2Dark: "#65a30d", HiDark: "#fde047", GreenDark: "#22c55e",
		BgLight: "#f7fee7", SurfaceLight: "#fcfdf2", TextLight: "#365314", MutedLight: "#65a30d",
		AccentLight: "#84cc16", Accent2Light: "#4d7c0f", HiLight: "#ca8a04",
		FontSans: "system-ui, Inter, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1rem", LineHeight: "1.6", MaxWidth: "42rem", RadiusSm: "0.375rem", RadiusLg: "0.5rem",
	}
}

// Rust — terracotta earth tones, grounded warmth.
func Rust() Tokens {
	return Tokens{
		Name: "Rust", BgDark: "#1a0e08", SurfaceDark: "#2d1710", TextDark: "#fef2f2",
		MutedDark: "#fca5a5", AccentDark: "#f87171", Accent2Dark: "#dc2626", HiDark: "#fb923c", GreenDark: "#86efac",
		BgLight: "#fff7ed", SurfaceLight: "#ffffff", TextLight: "#431407", MutedLight: "#c2410c",
		AccentLight: "#ea580c", Accent2Light: "#9a3412", HiLight: "#f97316",
		FontSans: "Georgia, Inter, serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.75", MaxWidth: "40rem", RadiusSm: "0.375rem", RadiusLg: "0.5rem",
	}
}

// Glacier — icy blue, clean and crisp.
func Glacier() Tokens {
	return Tokens{
		Name: "Glacier", BgDark: "#0a1628", SurfaceDark: "#102340", TextDark: "#e0f2fe",
		MutedDark: "#7dd3fc", AccentDark: "#38bdf8", Accent2Dark: "#0ea5e9", HiDark: "#22d3ee", GreenDark: "#34d399",
		BgLight: "#f0f9ff", SurfaceLight: "#ffffff", TextLight: "#0c4a6e", MutedLight: "#0284c7",
		AccentLight: "#0ea5e9", Accent2Light: "#0369a1", HiLight: "#06b6d4",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1rem", LineHeight: "1.65", MaxWidth: "44rem", RadiusSm: "0.5rem", RadiusLg: "0.625rem",
	}
}

// Coral — vibrant orange-pink, tropical energy.
func Coral() Tokens {
	return Tokens{
		Name: "Coral", BgDark: "#1a0a0a", SurfaceDark: "#2d1414", TextDark: "#fee2e2",
		MutedDark: "#fca5a5", AccentDark: "#fb7185", Accent2Dark: "#e11d48", HiDark: "#f97316", GreenDark: "#86efac",
		BgLight: "#fff1f2", SurfaceLight: "#ffffff", TextLight: "#4c0519", MutedLight: "#e11d48",
		AccentLight: "#f43f5e", Accent2Light: "#be123c", HiLight: "#ea580c",
		FontSans: "system-ui, Inter, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.7", MaxWidth: "42rem", RadiusSm: "0.5rem", RadiusLg: "0.75rem",
	}
}

// Gale — editorial magazine layout, bold typography, amber accents.
// Dark sophistication with large hero sections and card-based grids.
func Gale() Tokens {
	return Tokens{
		Name: "Gale", BgDark: "#111318", SurfaceDark: "#1a1d24", TextDark: "#e8eaed",
		MutedDark: "#8b919e", AccentDark: "#d4a853", Accent2Dark: "#f0d78c", HiDark: "#60a5fa", GreenDark: "#4ade80",
		BgLight: "#fafaf8", SurfaceLight: "#ffffff", TextLight: "#1a1a1a", MutedLight: "#6b6b6b",
		AccentLight: "#b8860b", Accent2Light: "#d4a853", HiLight: "#2563eb",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.125rem", LineHeight: "1.8", MaxWidth: "48rem", RadiusSm: "0.25rem", RadiusLg: "0.25rem",
		CustomCSS: galeCSS,
	}
}

// Zephyr — bright, creative, open layout with prominent CTAs.
// Coral-rose accents, airy spacing, multi-column footer.
func Zephyr() Tokens {
	return Tokens{
		Name: "Zephyr", BgDark: "#1a1b1e", SurfaceDark: "#24252d", TextDark: "#e4e5e9",
		MutedDark: "#8b8d97", AccentDark: "#ef767a", Accent2Dark: "#f9a0a3", HiDark: "#7ec8e3", GreenDark: "#6eeb83",
		BgLight: "#fefefa", SurfaceLight: "#ffffff", TextLight: "#1a1a1a", MutedLight: "#6b6b6b",
		AccentLight: "#e74c3c", Accent2Light: "#ef767a", HiLight: "#2980b9",
		FontSans: "Inter, system-ui, sans-serif", FontMono: "IBM Plex Mono, monospace",
		FontSizeBase: "1.0625rem", LineHeight: "1.75", MaxWidth: "44rem", RadiusSm: "0.5rem", RadiusLg: "0.75rem",
		CustomCSS: zephyrCSS,
	}
}
