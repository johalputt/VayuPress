// SPDX-License-Identifier: Apache-2.0

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

// SECTION 2 AUDIT — the part-count budget re-attacked, and it HELD. Recorded as
// a test rather than as a note, because the flat flood above does not actually
// prove the property the budget claims.
//
// maxMIMEParts is a WHOLE-MESSAGE budget: buildPart threads one counter through
// the recursion. TestParseMessagePartCountBounded only counts the root's direct
// children, so it pins that budget for a message that floods siblings at one
// level and says nothing about a message that floods at every level. Under a
// nested flood the root has a single child, and that assertion passes without
// looking at the 10,000 parts underneath it.
//
// The attack: a message whose containers each hold ten children, four levels
// deep — 10,000 leaf parts in 4.7 MB, comfortably inside the 25 MiB ingest cap,
// so it can be delivered and stored and then parsed on the victim's next FETCH.
//
// Result: 257 parts (the budget plus the root), parsed in single-digit
// milliseconds. The bound is global and it is real.
func countTreeParts(p *part) int {
	n := 1
	for _, c := range p.children {
		n += countTreeParts(c)
	}
	return n
}

func TestParseMessagePartBudgetIsWholeTreeNotPerLevel(t *testing.T) {
	const fanout, depth = 10, 4
	var build func(d int) string
	build = func(d int) string {
		b := fmt.Sprintf("b%d", d)
		s := "Content-Type: multipart/mixed; boundary=\"" + b + "\"\r\n\r\n"
		for i := 0; i < fanout; i++ {
			s += "--" + b + "\r\n"
			if d < depth {
				s += build(d + 1)
			} else {
				s += "Content-Type: text/plain\r\n\r\nleaf\r\n"
			}
		}
		return s + "--" + b + "--\r\n"
	}

	tree := parseMessage([]byte(build(0)))
	// +1 for the root, which is not itself charged to the budget.
	if got := countTreeParts(tree); got > maxMIMEParts+1 {
		t.Fatalf("a nested flood parsed to %d total parts, above the maxMIMEParts budget (%d).\n\n"+
			"The budget is meant to cover the whole message. If it is spent per container "+
			"instead, a message that nests as well as floods multiplies straight past it — "+
			"and this parser runs on the IMAP listener, which has no Recoverer, so the "+
			"process it takes down is the whole install.", got, maxMIMEParts)
	}
	// The flood really did nest, or the assertion above proved nothing about nesting.
	if len(tree.children) != 1 {
		t.Fatalf("fixture wrong: root has %d children, want 1 nested container", len(tree.children))
	}
}
