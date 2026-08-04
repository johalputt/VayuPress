// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// DraftStatus is the ONLY article status a VayuFlow content action may cause.
//
// This constant is load-bearing rather than decorative. An article whose status
// is the empty string is treated as PUBLISHED — internal/db/article_repo.go
// defaults `""` to "published" on insert, and the read path COALESCEs it the
// same way. So for content, the safe value is not the zero value, and a
// forgotten field publishes rather than fails.
//
// internal/api's ArticleService.Create builds its Article without setting
// Status at all, which is correct for the editor (an author pressing Publish)
// and would be exactly wrong here. VayuFlow therefore does not use that path.
const DraftStatus = "draft"

// Draft is what a content action may write.
//
// There is deliberately NO Status field. A guard that checked a status would be
// one forgotten branch away from publishing; a type that cannot express
// "published" cannot publish however the action is written, and that is the
// difference between a rule and a property. §6's clamp on model output relies
// on the same idea one level up.
type Draft struct {
	Slug    string
	Title   string
	Content string
	Tags    []string
}

// ContentWriter is the narrow slice of article storage this engine needs.
//
// Implementations MUST persist with DraftStatus and must not accept a status
// from the caller — there is nowhere in Draft for one to come from.
type ContentWriter interface {
	// CreateDraft persists a new unpublished article.
	CreateDraft(ctx context.Context, d Draft) error
	// UpdateDraft edits an existing article and must refuse if that article is
	// not itself a draft. Editing a published post is a live write, which no
	// v1 content action is permitted to perform.
	UpdateDraft(ctx context.Context, d Draft) error
}

var (
	contentMu sync.RWMutex
	content   ContentWriter
)

// SetContentWriter wires the storage the content actions use. It is called once
// at startup; passing nil detaches them, which the tests use.
func SetContentWriter(w ContentWriter) {
	contentMu.Lock()
	defer contentMu.Unlock()
	content = w
}

func contentWriter() (ContentWriter, error) {
	contentMu.RLock()
	defer contentMu.RUnlock()
	if content == nil {
		return nil, fmt.Errorf("vayuflow: no content storage is wired into this build")
	}
	return content, nil
}

// draftFromParams builds a Draft from a step's params, refusing anything that
// would produce an article a reader could not make sense of.
func draftFromParams(p map[string]string) (Draft, error) {
	d := Draft{
		Slug:    strings.TrimSpace(p["slug"]),
		Title:   strings.TrimSpace(p["title"]),
		Content: p["content"],
	}
	if raw := strings.TrimSpace(p["tags"]); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				d.Tags = append(d.Tags, t)
			}
		}
	}
	if d.Title == "" {
		return Draft{}, fmt.Errorf("vayuflow: a draft needs a title")
	}
	if d.Slug == "" {
		return Draft{}, fmt.Errorf("vayuflow: a draft needs a slug")
	}
	return d, nil
}

func init() {
	RegisterAction("content.draft.create", func(ctx context.Context, p map[string]string, e *Effects) (string, error) {
		d, err := draftFromParams(p)
		if err != nil {
			return "", err
		}
		// Permission first, effect second. The charge happens inside Write, so
		// an action cannot cause a change it did not ask for.
		if err := e.Write(WriteDraft, "create draft "+d.Slug+" — "+d.Title); err != nil {
			return "", err
		}
		w, err := contentWriter()
		if err != nil {
			return "", err
		}
		if err := w.CreateDraft(ctx, d); err != nil {
			return "", err
		}
		return "created draft " + d.Slug, nil
	})

	RegisterAction("content.draft.update", func(ctx context.Context, p map[string]string, e *Effects) (string, error) {
		d, err := draftFromParams(p)
		if err != nil {
			return "", err
		}
		if err := e.Write(WriteDraft, "update draft "+d.Slug); err != nil {
			return "", err
		}
		w, err := contentWriter()
		if err != nil {
			return "", err
		}
		if err := w.UpdateDraft(ctx, d); err != nil {
			return "", err
		}
		return "updated draft " + d.Slug, nil
	})
}
