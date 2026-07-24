package mail

import (
	"fmt"
	"testing"
)

// nestMultipart builds a message whose body is `depth` correctly-closed
// multipart/mixed containers nested one inside the next — the shape a crafted
// message uses to drive unbounded recursion in the FETCH parser (audit H5).
func nestMultipart(depth int) string {
	if depth == 0 {
		return "Content-Type: text/plain\r\n\r\nhi\r\n"
	}
	b := fmt.Sprintf("bound%d", depth)
	inner := nestMultipart(depth - 1)
	return fmt.Sprintf("Content-Type: multipart/mixed; boundary=%s\r\n\r\n--%s\r\n%s\r\n--%s--\r\n", b, b, inner, b)
}

func treeDepth(p *part) int {
	max := 0
	for _, c := range p.children {
		if cd := treeDepth(c); cd > max {
			max = cd
		}
	}
	return max + 1
}

// TestParseMessageDepthBounded guards audit H5: a deeply nested message must
// parse to a finite tree (bounded by maxMIMEDepth) instead of recursing until the
// process stack-overflows or OOMs. A crash/hang here means the bound regressed.
func TestParseMessageDepthBounded(t *testing.T) {
	tree := parseMessage([]byte(nestMultipart(200)))
	if d := treeDepth(tree); d > maxMIMEDepth+1 {
		t.Fatalf("parsed tree depth %d exceeds the maxMIMEDepth bound (%d)", d, maxMIMEDepth+1)
	}
}

// TestParseMessagePartCountBounded guards the companion part-count cap: a message
// that floods sibling parts must not build an unbounded child slice.
func TestParseMessagePartCountBounded(t *testing.T) {
	var body string
	for i := 0; i < maxMIMEParts+500; i++ {
		body += "--b\r\nContent-Type: text/plain\r\n\r\nx\r\n"
	}
	msg := "Content-Type: multipart/mixed; boundary=b\r\n\r\n" + body + "--b--\r\n"
	tree := parseMessage([]byte(msg))
	if len(tree.children) > maxMIMEParts {
		t.Fatalf("parsed %d parts, exceeds maxMIMEParts bound (%d)", len(tree.children), maxMIMEParts)
	}
}
