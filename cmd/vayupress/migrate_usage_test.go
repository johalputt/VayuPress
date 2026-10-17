// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// captureStdout runs f and returns what it printed.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = orig
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

var flagName = regexp.MustCompile(`--([a-z][a-z-]*)`)

// The usage once offered --db, which nothing parsed: an import aimed at another
// database went to the service's. Every flag the usage names must be one a
// migrate subcommand reads, judged from the parse calls in the source rather
// than from the usage itself.
func TestMigrateUsageNamesOnlyFlagsThatAreParsed(t *testing.T) {
	var src strings.Builder
	for _, f := range []string{"migrate.go", "migrate_import.go", "content_bundle.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	usage := captureStdout(t, printMigrateUsage)
	for _, m := range flagName.FindAllStringSubmatch(usage, -1) {
		if !regexp.MustCompile(`parse(String|Bool)Flag\(args, "` + m[1] + `"`).MatchString(src.String()) {
			t.Errorf("vayupress migrate's usage offers --%s, which no subcommand parses", m[1])
		}
	}
}

// migrate info points at the standalone importers. Its flags were written for
// tools that took --wp-db, --ghost-db, --dir and --zip; the tools take
// --wp-dsn, --ghost-dsn, --site and --input, so every line was a command that
// failed. Each flag on a tool's line must be one that tool's source defines.
func TestMigrateInfoNamesEachToolsRealFlags(t *testing.T) {
	info := captureStdout(t, printMigrateInfo)
	line := regexp.MustCompile(`(?m)^\s+\S+\s+(tools/\S+)\s+(.*)$`)
	rows := line.FindAllStringSubmatch(info, -1)
	if len(rows) < 7 {
		t.Fatalf("found %d tool rows in migrate info, want the seven importers:\n%s", len(rows), info)
	}
	for _, row := range rows {
		var src strings.Builder
		_ = filepath.WalkDir(filepath.Join("..", "..", row[1]), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				b, _ := os.ReadFile(p)
				src.Write(b)
			}
			return nil
		})
		if src.Len() == 0 {
			t.Errorf("migrate info names %s, which has no source", row[1])
			continue
		}
		for _, m := range flagName.FindAllStringSubmatch(row[2], -1) {
			if !strings.Contains(src.String(), `"`+m[1]+`", `) {
				t.Errorf("migrate info tells %s users to pass --%s, which the tool does not define", row[1], m[1])
			}
		}
	}
}
