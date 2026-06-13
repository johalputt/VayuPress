//go:build !linux

package sandbox

// PluginConfinement is a no-op on non-Linux platforms.
type PluginConfinement struct{}

func SetupConfinement(_ Manifest) *PluginConfinement       { return &PluginConfinement{} }
func (c *PluginConfinement) ScratchDir() string            { return "" }
func (c *PluginConfinement) Cleanup()                      {}
func ApplySeccompFilter() error                            { return nil }
func DropCapabilities() error                              { return nil }
func CloseExtraFDs(_ []int)                                {}
func MountNamespaceFlags(_ Manifest) uintptr               { return 0 }
func PrepareExecEnv(m Manifest, _ string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"PLUGIN_NAME=" + m.Name,
	}
	return append(env, m.Env...)
}
