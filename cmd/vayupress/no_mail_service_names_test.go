// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mailServiceName is another mail service, by any of its names. The
// product names none of them: not in its pages, its docs, its changelog or
// its comments. Mail programs a person connects with (Thunderbird, Apple
// Mail, K-9) are not services and are not here.
var mailServiceName = regexp.MustCompile(`(?i)\b(gmail|googlemail|proton ?mail|outlook|hotmail|yahoo ?mail|icloud|fastmail|tutanota|tuta\.io|zoho ?mail|superhuman)\b|\bhey\.com\b`)

// notProse is text that names a service without being about it, taken out
// of a line before it is judged: the OpenPGP library's import path and its
// repository (whose advisories secwatch reads), its copyright holder as the
// licence requires it to be written, the referrer
// hostnames Analytics must recognise, and the operator's own address in CI.
var notProse = strings.NewReplacer(
	"github.com/ProtonMail/go-crypto", "", "ProtonMail/go-crypto", "",
	"Copyright (c) The ProtonMail Authors", "",
	"mail.google.com", "", "outlook.live.com", "", "outlook.office.com", "", "mail.yahoo.com", "", "mail.proton.me", "",
	"ankushchoudharyjohal@gmail.com", "",
)

// textFile is a file whose words are the product's: source, pages,
// stylesheets, scripts, docs and configuration.
func textFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".md", ".markdown", ".js", ".css", ".html", ".tmpl", ".sh", ".yml", ".yaml", ".json", ".txt", ".conf", ".sql", ".example", ".toml", ".svg", ".xml":
		return true
	}
	switch filepath.Base(path) {
	case "NOTICE", "README", "LICENSE", "Makefile", "Dockerfile", "Caddyfile", "Caddyfile.production":
		return true
	}
	return false
}

func TestNoOtherMailServiceIsNamed(t *testing.T) {
	root := filepath.Join("..", "..")
	self, _ := filepath.Abs("no_mail_service_names_test.go")
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "test-results", "playwright-report":
				return filepath.SkipDir
			}
			return nil
		}
		if abs, _ := filepath.Abs(path); abs == self || !textFile(path) {
			return nil
		}
		f, err := os.Open(path) // #nosec G304 -- a file of this repository
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for n := 1; sc.Scan(); n++ {
			if m := mailServiceName.FindString(notProse.Replace(sc.Text())); m != "" {
				rel, _ := filepath.Rel(root, path)
				found = append(found, rel+":"+itoaSafe(n)+": "+m)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("another mail service is named in the product (say what it is, not whose it is):\n  %s", strings.Join(found, "\n  "))
	}
}
