// SPDX-License-Identifier: Apache-2.0

package blockrender

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

// An SVG an author uploads is drawn on every reader's page. What the
// sanitiser lets through is checked here by reading the output again, not by
// asking the sanitiser: no element that runs or embeds anything, no event
// attribute, no reference that leaves the document, and a second pass
// changes nothing (an output that sanitises differently was not finished).
func FuzzSanitizeSVG(f *testing.F) {
	for _, s := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="5" height="5" fill="red"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script><circle r="1" onload="x()"/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="https://evil.example/a.svg#x"/><a href="javascript:x()"><text>t</text></a></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">x</div></foreignObject><rect style="fill:url(https://evil.example/)"/></svg>`,
		`<svg><scr<script>ipt>alert(1)</script></svg>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		out, ok := SanitizeSVG(src)
		if !ok {
			return
		}
		dec := xml.NewDecoder(strings.NewReader(out))
		dec.Strict = true
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("the sanitised SVG does not parse: %v\n%s", err, out)
			}
			el, isStart := tok.(xml.StartElement)
			if !isStart {
				continue
			}
			switch strings.ToLower(el.Name.Local) {
			case "script", "foreignobject", "iframe", "embed", "object", "image", "animate", "set", "handler", "listener":
				t.Fatalf("<%s> got through:\n%s", el.Name.Local, out)
			}
			for _, a := range el.Attr {
				name, val := strings.ToLower(a.Name.Local), strings.ToLower(strings.TrimSpace(a.Value))
				if strings.HasPrefix(name, "on") {
					t.Fatalf("the event attribute %s got through:\n%s", a.Name.Local, out)
				}
				if name == "href" && !strings.HasPrefix(val, "#") {
					t.Fatalf("an href that leaves the document got through: %q", a.Value)
				}
				if strings.Contains(val, "javascript:") || strings.Contains(val, "url(") && !strings.Contains(val, "url(#") ||
					strings.Contains(val, "image-set(") || strings.Contains(val, `\`) {
					t.Fatalf("%s=%q got through", a.Name.Local, a.Value)
				}
			}
		}
		again, ok := SanitizeSVG(out)
		if !ok || again != out {
			t.Fatalf("a second pass changed the sanitised SVG:\n%s\n%s", out, again)
		}
	})
}
