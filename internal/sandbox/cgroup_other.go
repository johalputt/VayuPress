//go:build !linux

package sandbox

func setupCgroup(m Manifest, pid int) func() { return func() {} }
