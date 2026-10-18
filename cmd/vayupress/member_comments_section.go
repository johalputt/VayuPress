// SPDX-License-Identifier: Apache-2.0

package main

// member_comments_section.go — "Comments", opened in place on /members/account.
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

// memberCommentsSection lists the member's own comments with an honest status
// for each, and returns their count with how many are live and in review. The
// list is empty when the member has never commented, so no empty row is drawn.
func (a *App) memberCommentsSection(ctx context.Context, m *members.Member) (body string, n, live, review int) {
	list := a.memberOwnComments(ctx, m)
	if len(list) == 0 {
		return "", 0, 0, 0
	}
	var b strings.Builder
	b.WriteString(`<ul class="acct-list">`)
	for i := range list {
		c := list[i]
		switch c.Status {
		case comments.StatusApproved:
			live++
		case comments.StatusPending:
			review++
		}

		// A comment on a deleted post has no slug, so it gets no link rather than a
		// link to "/" — which would look like the post moved rather than went away.
		where := `<span class="acct-list__where">On a post that has since been removed</span>`
		if c.Slug != "" {
			title := c.Title
			if strings.TrimSpace(title) == "" {
				title = c.Slug
			}
			where = `<a class="acct-list__where" href="/` + html.EscapeString(c.Slug) +
				`#comments">On &ldquo;` + html.EscapeString(title) + `&rdquo;</a>`
		}

		b.WriteString(`<li><div class="acct-list__top">` + where + commentStatusChip(c.Status) + `</div>` +
			`<p class="acct-list__body">` + html.EscapeString(truncateComment(c.Body)) + `</p>` +
			`<span class="acct-list__meta">` + html.EscapeString(config.FormatSite(c.CreatedAt, "2 Jan 2006")) + `</span></li>`)
	}
	b.WriteString(`</ul>`)
	if review > 0 {
		b.WriteString(`<p class="acct-more__foot">` + countOf(review, "comment") +
			` awaiting review. Comments are read by a person, so this can take a little while.</p>`)
	}
	return b.String(), len(list), live, review
}

// memberCommentCounts counts the member's comments as the account page lists
// them; ok is false when they have none.
func (a *App) memberCommentCounts(ctx context.Context, m *members.Member) (live, review int, ok bool) {
	list := a.memberOwnComments(ctx, m)
	for i := range list {
		switch list[i].Status {
		case comments.StatusApproved:
			live++
		case comments.StatusPending:
			review++
		}
	}
	return live, review, len(list) > 0
}

func (a *App) memberOwnComments(ctx context.Context, m *members.Member) []comments.MemberComment {
	if a.commentStore == nil || m == nil {
		return nil
	}
	list, err := a.commentStore.ListByEmail(ctx, dbpkg.Reader(), m.Email, maxMemberComments)
	if err != nil {
		return nil
	}
	return list
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
		return acctState("Live", "ok")
	case comments.StatusPending:
		return acctState("In review", "off")
	case comments.StatusRejected, comments.StatusSpam:
		return acctState("Not approved", "warn")
	default:
		return acctState(status, "off")
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
