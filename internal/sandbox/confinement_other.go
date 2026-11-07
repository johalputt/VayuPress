// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package sandbox

import "os/exec"

// PluginConfinement is a no-op on non-Linux platforms.
type PluginConfinement struct{}

func SetupConfinement(_ Manifest) (*PluginConfinement, error) { return &PluginConfinement{}, nil }
func (c *PluginConfinement) ScratchDir() string               { return "" }
func (c *PluginConfinement) Cleanup()                         {}
func CloseExtraFDs(_ []int)                                   {}
func MountNamespaceFlags(_ Manifest) uintptr                  { return 0 }

// RunHelperIfAsked has no child step to run off Linux.
func RunHelperIfAsked() {}

// pluginCommand starts the plugin directly: off Linux there is no filter,
// capability set or namespace to apply.
func pluginCommand(m Manifest, _ *PluginConfinement, _ bool) *exec.Cmd {
	return exec.Command(m.Executable, m.Args...)
}

func PrepareExecEnv(m Manifest, _ string) []string {
	env := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"PLUGIN_NAME=" + m.Name,
	}
	return append(env, m.Env...)
}
