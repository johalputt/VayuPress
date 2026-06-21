package theme

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
	}
}
