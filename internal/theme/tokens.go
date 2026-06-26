package theme

// Tokens holds the full set of design tokens for a VayuPress theme.
// Dark-mode tokens apply when prefers-color-scheme is dark (the default);
// light-mode tokens apply via the @media override.
// CustomCSS is optional per-preset CSS injected alongside the token stylesheet.
type Tokens struct {
	Name string

	// Dark mode
	BgDark      string
	SurfaceDark string
	TextDark    string
	MutedDark   string
	AccentDark  string
	Accent2Dark string
	HiDark      string
	GreenDark   string

	// Light mode
	BgLight      string
	SurfaceLight string
	TextLight    string
	MutedLight   string
	AccentLight  string
	Accent2Light string
	HiLight      string

	// Typography
	FontSans     string
	FontMono     string
	FontSizeBase string
	LineHeight   string

	// Layout
	MaxWidth string
	RadiusSm string
	RadiusLg string

	// Per-preset CSS (optional) — injected into /theme.css when this preset is active.
	CustomCSS string `json:"custom_css,omitempty"`

	// Options holds theme-level customization choices (color scheme, reading
	// width, corner style, heading case, accent fill, …). They are applied by
	// CompileCSS — mostly by mutating the tokens above, plus a little scoped CSS
	// — so every theme is customizable along the same dimensions without editing
	// raw CSS. See options.go for the available keys/values.
	Options map[string]string `json:"options,omitempty"`
}
