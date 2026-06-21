package theme

// Tokens holds the full set of design tokens for a VayuPress theme.
// Dark-mode tokens apply when prefers-color-scheme is dark (the default);
// light-mode tokens apply via the @media override.
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
}
