// SPDX-License-Identifier: Apache-2.0

package hoststat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadingsFromProcFiles(t *testing.T) {
	dir := t.TempDir()
	prev := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = prev })
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("meminfo", "MemTotal:       16000 kB\nMemFree:  100 kB\nMemAvailable:    4000 kB\n")
	write("self/status", "Name: x\nVmRSS:\t   512 kB\n")
	write("pressure/io", "some avg10=29.64 avg60=12.54 avg300=9.14 total=292298917\nfull avg10=4.00 avg60=0.00 avg300=0.00 total=0\n")
	write("loadavg", "3.42 2.60 2.83 10/148 18820\n")

	if total, avail, ok := MemInfo(); !ok || total != 16000*1024 || avail != 4000*1024 {
		t.Errorf("MemInfo = %d %d %v", total, avail, ok)
	}
	if rss := ProcRSS(); rss != 512*1024 {
		t.Errorf("ProcRSS = %d", rss)
	}
	if v, ok := Pressure("io"); !ok || v != 29.64 {
		t.Errorf("Pressure(io) = %v %v; want the some line's avg10, not the full line's", v, ok)
	}
	if _, ok := Pressure("cpu"); ok {
		t.Error("a kernel without cpu PSI reported a reading")
	}
	if v, ok := Load1(); !ok || v != 3.42 {
		t.Errorf("Load1 = %v %v", v, ok)
	}
	if _, avail := parseMemInfo(strings.NewReader("MemTotal: 10 kB\n")); avail != 0 {
		t.Errorf("a missing MemAvailable read as %d", avail)
	}
}
