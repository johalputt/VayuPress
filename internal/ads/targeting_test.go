// SPDX-License-Identifier: Apache-2.0

package ads

import (
	"reflect"
	"testing"
)

// A slot shows where it is set to: everywhere by default, posts only, pages
// only, or the addresses listed. The home page is neither a post nor a page.
func TestASlotShowsWhereItIsSet(t *testing.T) {
	home, post, page := Page{Path: "/"}, Page{Path: "/a-post", Post: true}, Page{Path: "/about", IsPage: true}
	for _, tc := range []struct {
		slot             Slot
		home, post, page bool
	}{
		{Slot{}, true, true, true},
		{Slot{ShowOn: ShowPosts}, false, true, false},
		{Slot{ShowOn: ShowPages}, false, false, true},
		{Slot{ShowOn: ShowChosen, ShowPaths: []string{"/about"}}, false, false, true},
		{Slot{ShowOn: ShowChosen, ShowPaths: []string{"/"}}, true, false, false},
	} {
		for _, c := range []struct {
			p    Page
			want bool
		}{{home, tc.home}, {post, tc.post}, {page, tc.page}} {
			if got := tc.slot.ShowsOn(c.p); got != c.want {
				t.Errorf("a slot set to %q %v shows on %s: %v, want %v", tc.slot.ShowOn, tc.slot.ShowPaths, c.p.Path, got, c.want)
			}
		}
	}
}

// An address is read however it is typed or pasted.
func TestAddressesAreReadAsTyped(t *testing.T) {
	got := Paths("about\n/a-post/\r\nhttps://example.com/news?x=1, /about,\n\n /")
	want := []string{"/about", "/a-post", "/news", "/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Paths read %q, want %q", got, want)
	}
	if !HasPath([]string{"/news"}, "/news/") || HasPath([]string{"/news"}, "/news-2") {
		t.Error("HasPath does not match an address as Paths reads it")
	}
}

// A slot's choice of pages is one the store can hold: a known choice, and a
// list when the choice is a list.
func TestASlotsChoiceOfPagesIsChecked(t *testing.T) {
	base := SlotInput{Name: "x", Placement: PlacementHeader, Kind: KindImage}
	bad := base
	bad.ShowOn = "sometimes"
	if _, _, err := bad.check(); err == nil {
		t.Error("an unknown choice of pages was accepted")
	}
	empty := base
	empty.ShowOn = ShowChosen
	if _, _, err := empty.check(); err == nil {
		t.Error("a list choice with no addresses was accepted")
	}
	ok := base
	ok.ShowOn, ok.ShowPaths = ShowChosen, "about\n/about/"
	if _, paths, err := ok.check(); err != nil || paths != "/about" {
		t.Errorf("a listed choice stored %q (err %v), want \"/about\"", paths, err)
	}
}
