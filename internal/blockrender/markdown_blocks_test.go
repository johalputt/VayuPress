// SPDX-License-Identifier: Apache-2.0

package blockrender

import (
	"strings"
	"testing"
)

// TestMarkdownToBlocks verifies a Markdown draft becomes multiple editable
// blocks whose text survives the round-trip.
func TestMarkdownToBlocks(t *testing.T) {
	blocks := MarkdownToBlocks("# Title\n\nHello world, this is a paragraph.\n\n- one\n- two\n")
	if len(blocks) == 0 {
		t.Fatal("expected at least one block")
	}
	var all strings.Builder
	for _, b := range blocks {
		all.WriteString(b.Text)
		all.WriteString(" ")
		for _, it := range b.Items {
			all.WriteString(it)
			all.WriteString(" ")
		}
	}
	joined := all.String()
	if !strings.Contains(joined, "Hello world") {
		t.Errorf("paragraph text lost: %q (blocks=%+v)", joined, blocks)
	}
	if !strings.Contains(joined, "Title") {
		t.Errorf("title text lost: %q", joined)
	}
}

// TestMarkdownToBlocksFallback ensures unparseable/empty input still yields a
// block (never a nil/empty draft that would lose the model's output).
func TestMarkdownToBlocksFallback(t *testing.T) {
	if got := MarkdownToBlocks("just one line"); len(got) == 0 {
		t.Error("expected a fallback block for plain text")
	}
}
