// SPDX-License-Identifier: Apache-2.0

package main

// member_comments_section.go — "Your comments", inline on /members/account.
//
// This existed already, but only inside the floating VayuPortal drawer, reached
// by a button that switches to an in-drawer view — so a member who navigated to
// their account page could not get to it from there at all. The account page is
// where someone goes to see their own stuff; this renders it in place.
//
// The store method and the article join already existed (comments.ListByEmail);
// what was missing was a member-page rendering of it.

import (
	"context"
	"html"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/comments"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/members"
)

const (
	maxMemberComments = 20
	// commentPreviewLen keeps a row scannable. The full comment is on the post,
	// which is one click away.
	commentPreviewLen = 180
)

// memberCommentsSection renders the member's own comments with an honest status
// for each, plus the accordion summary line and chip. Empty strings when the
// member has never commented, so no empty row is rendered.
func (a *App) memberCommentsSection(ctx context.Context, m *members.Member) (body, sub, chip string) {
	if a.commentStore == nil || m == nil {
		return "", "", ""
	}
	list, err := a.commentStore.ListByEmail(ctx, dbpkg.Reader(), m.Email, maxMemberComments)
	if err != nil || len(list) == 0 {
		return "", "", ""
	}

	live, waiting := 0, 0
	var b strings.Builder
	b.WriteString(`<section class="ma-card">
    <h2>Your comments</h2>
    <p class="ma-hint">Where you commented, and whether each one is live yet.</p>
    <ul class="ma-cmts">`)
	for i := range list {
		c := list[i]
		switch c.Status {
		case comments.StatusApproved:
			live++
		case comments.StatusPending:
			waiting++
		}

		// A comment on a deleted post has no slug, so it gets no link rather than a
		// link to "/" — which would look like the post moved rather than went away.
		where := `<span class="ma-cmt__where ma-cmt__where--gone">on a post that has since been removed</span>`
		if c.Slug != "" {
			title := c.Title
			if strings.TrimSpace(title) == "" {
				title = c.Slug
			}
			where = `<a class="ma-cmt__where su-link" href="/` + html.EscapeString(c.Slug) +
				`#comments">on &ldquo;` + html.EscapeString(title) + `&rdquo; &rarr;</a>`
		}

		b.WriteString(`<li class="ma-cmt">
      <div class="ma-cmt__top">
        <span class="ma-cmt__when">` + html.EscapeString(config.FormatSite(c.CreatedAt, "2 Jan 2006")) + `</span>
        ` + commentStatusChip(c.Status) + `
      </div>
      <p class="ma-cmt__body">` + html.EscapeString(truncateComment(c.Body)) + `</p>
      ` + where + `
    </li>`)
	}
	b.WriteString(`</ul>`)
	if waiting > 0 {
		b.WriteString(`<p class="ma-hint">` + countOf(waiting, "comment") +
			` awaiting review. Comments are read by a person, so this can take a little while.</p>`)
	}
	b.WriteString(`
  </section>`)

	sub = countOf(len(list), "comment")
	chip = maChip(strconv.Itoa(live)+" live", live > 0)
	return b.String(), sub, chip
}

// commentStatusChip labels a comment's moderation state for its own author.
//
// Spam and rejected deliberately collapse to the same "Not approved" — the same
// choice the portal drawer already makes. Telling an author "you were marked as
// spam" hands a spammer a tuning signal, and tells a misclassified human
// something insulting and unactionable. Nothing is hidden, though: a member sees
// every comment they wrote and its real state, because a comment that silently
// never appears is the thing people write in to ask about.
func commentStatusChip(status string) string {
	switch status {
	case comments.StatusApproved:
		return maChip("Live", true)
	case comments.StatusPending:
		return maChip("In review", false)
	case comments.StatusRejected, comments.StatusSpam:
		return maChip("Not approved", false)
	default:
		return maChip(status, false)
	}
}

// truncateComment shortens a preview on a word boundary where it can, so a row
// does not end mid-word. The ellipsis is only added when something was actually
// cut.
func truncateComment(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= commentPreviewLen {
		return s
	}
	cut := s[:commentPreviewLen]
	if i := strings.LastIndex(cut, " "); i > commentPreviewLen/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
