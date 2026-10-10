// SPDX-License-Identifier: Apache-2.0

package render

import (
	"strings"
	"testing"
)

// A tag page with fewer than thinTagPosts posts is kept out of search, its
// links still followed; one with that many is offered as before.
func TestAThinTagPageIsNotIndexed(t *testing.T) {
	const noindex = `<meta name="robots" content="noindex,follow">`
	for n, want := range map[int]bool{1: true, thinTagPosts - 1: true, thinTagPosts: false, 40: false} {
		page, err := RenderTagPage("example.com", "1.0.0", "go", nil, n)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(page, noindex); got != want {
			t.Errorf("%d posts: noindex %v, want %v", n, got, want)
		}
	}
}
