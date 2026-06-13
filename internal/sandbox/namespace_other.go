//go:build !linux

package sandbox

func namespaceCloneflags(m Manifest) uintptr { return 0 }
