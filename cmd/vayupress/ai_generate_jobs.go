package main

// ai_generate_jobs.go — draft generation as a background job.
//
// Generation used to be one long synchronous request. That works against a fast
// hosted model and fails against exactly the models people reach for first: a
// large free model on OpenRouter can take minutes, and any reverse proxy or CDN in
// front of VayuPress closes the connection long before our own 120s client
// timeout. The browser then gets the PROXY's error page — an HTML 502 with no
// VayuPress JSON in it — so the panel could only report "Generation failed
// (HTTP 502)". The request had in fact been sent, and the model may even have
// answered after the connection died.
//
// So the wait is no longer held open. The POST starts a job and returns
// immediately; the panel polls for the result. No intermediary has a long-lived
// connection to time out, and a slow model is simply a slow job.
//
// Jobs live in memory only. They are single-use, short-lived scratch state for a
// draft the author has not accepted yet — nothing here is worth a table, and a
// restart losing an in-flight draft is the same outcome as a failed request.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/logging"
)

// Job lifecycle bounds.
const (
	// aiJobTTL is how long a finished job stays readable. It only has to outlive
	// the panel's poll loop plus a page reload.
	aiJobTTL = 10 * time.Minute
	// aiJobMaxRun caps a single generation: ten minutes of actual model time.
	// Generous on purpose — the models people reach for first are large free ones
	// that sit in a provider queue — but bounded so a hung provider cannot pin a
	// slot forever. aiGenHTTP's client timeout is tied to this value so the two
	// cannot drift apart and quietly cap generation earlier than the job allows.
	aiJobMaxRun = 10 * time.Minute
	// aiJobMax bounds the store so a burst cannot grow it without limit.
	aiJobMax = 200
)

// Job states.
const (
	aiJobPending = "pending"
	aiJobDone    = "done"
	aiJobError   = "error"
)

// aiJob is one draft generation.
type aiJob struct {
	ID     string
	Owner  string // console user id; only the owner may read the result
	Status string
	// Message carries the failure reason for aiJobError. It is already safe to
	// display: the runner decides what may be disclosed before storing it.
	Message string
	// Queued is true while the job is waiting for a concurrency slot rather than
	// waiting on the model. The distinction matters to the author: "queued behind
	// other drafts" is their own install being busy, "writing" is the provider.
	Queued   bool
	Blocks   []blockrender.Block
	Markdown string
	Notes    []string
	Started  time.Time
	Done     time.Time
}

var (
	aiJobMu sync.Mutex
	aiJobs  = map[string]*aiJob{}
)

// aiJobPut stores a job, first evicting anything expired. Eviction happens on
// write rather than on a timer so there is no goroutine to leak and no work done
// on an install where nobody uses the feature.
func aiJobPut(j *aiJob) {
	aiJobMu.Lock()
	defer aiJobMu.Unlock()
	now := time.Now()
	for id, old := range aiJobs {
		switch {
		case old.Status != aiJobPending && now.Sub(old.Done) > aiJobTTL:
			delete(aiJobs, id)
		case old.Status == aiJobPending && now.Sub(old.Started) > aiJobMaxRun+time.Minute:
			// A pending job older than its own hard cap means the runner died
			// without recording an outcome. Drop it rather than leave the panel
			// polling something that will never resolve.
			delete(aiJobs, id)
		}
	}
	if len(aiJobs) >= aiJobMax {
		// Evict the oldest to make room. At this size the install is under a burst
		// the rate limiter should already be shaping.
		var oldestID string
		var oldest time.Time
		for id, old := range aiJobs {
			if oldestID == "" || old.Started.Before(oldest) {
				oldestID, oldest = id, old.Started
			}
		}
		delete(aiJobs, oldestID)
	}
	aiJobs[j.ID] = j
}

// aiJobGet returns a copy of the job for owner, or ok=false.
//
// The owner check is the access control on generated content: a draft is written
// from one author's prompt and must not be readable by another console user who
// guesses an id.
func aiJobGet(id, owner string) (aiJob, bool) {
	aiJobMu.Lock()
	defer aiJobMu.Unlock()
	j, ok := aiJobs[id]
	if !ok || j.Owner != owner {
		return aiJob{}, false
	}
	return *j, true
}

// aiJobFinish records a TERMINAL outcome and stamps the completion time, which is
// what the TTL sweep measures from.
func aiJobFinish(id string, apply func(*aiJob)) {
	aiJobMu.Lock()
	defer aiJobMu.Unlock()
	j, ok := aiJobs[id]
	if !ok {
		return
	}
	apply(j)
	j.Done = time.Now()
}

// aiJobUpdate mutates a job that is still running. Kept separate from
// aiJobFinish so progress updates cannot stamp a completion time on a job that
// has not completed.
func aiJobUpdate(id string, apply func(*aiJob)) {
	aiJobMu.Lock()
	defer aiJobMu.Unlock()
	if j, ok := aiJobs[id]; ok {
		apply(j)
	}
}

// runAIJob performs the generation and records the outcome. It runs detached from
// the HTTP request, so it must NOT use the request's context — that is cancelled
// the moment the POST returns, which would abort every job instantly.
func (a *App) runAIJob(id string, backend aiassist.Backend, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), aiJobMaxRun)
	defer cancel()

	// Concurrency cap, as before. Waiting here is fine now: nothing is holding a
	// connection open while this job queues.
	select {
	case aiGenSlots <- struct{}{}:
		defer func() { <-aiGenSlots }()
		aiJobUpdate(id, func(j *aiJob) { j.Queued = false })
	case <-ctx.Done():
		aiJobFinish(id, func(j *aiJob) {
			j.Status = aiJobError
			j.Message = "The AI service stayed busy for too long. Try again in a moment."
		})
		return
	}

	md, meta, err := aiassist.GenerateOpDetail(ctx, aiGenHTTP, backend, aiassist.OpDraft, prompt)
	if err != nil {
		logging.LogError("ai-generate", "provider generation failed", err.Error())
		aiJobFinish(id, func(j *aiJob) {
			j.Status = aiJobError
			j.Message = aiFailureMessage(err, ctx.Err() != nil)
		})
		return
	}
	// The prompt asks for HTML, but models ignore format instructions often enough
	// that the format is detected rather than assumed. Importing HTML through the
	// Markdown parser collapses a whole article into one paragraph of tag soup,
	// which looks like a real draft until the author reads it.
	var blocks []blockrender.Block
	if aiassist.LooksLikeHTML(md) {
		blocks = blockrender.ImportHTML(md)
	} else {
		blocks = blockrender.MarkdownToBlocks(md)
	}
	if len(blocks) == 0 {
		logging.LogError("ai-generate", "model output parsed to zero blocks", "chars="+strconv.Itoa(len(md)))
		aiJobFinish(id, func(j *aiJob) {
			j.Status = aiJobError
			j.Message = "The model replied, but the reply contained no usable text. Try again, or pick a different model."
		})
		return
	}
	notes := aiDraftNotes(meta, backend.Model)
	aiJobFinish(id, func(j *aiJob) {
		j.Status = aiJobDone
		j.Blocks = blocks
		j.Markdown = md
		j.Notes = notes
	})
}

// aiDraftNotes describes anything about a successful draft the author should know
// before publishing it.
func aiDraftNotes(meta aiassist.Meta, asked string) []string {
	var notes []string
	if meta.Truncated {
		notes = append(notes, "the model hit its length limit, so the draft stops early — raise the length or continue it yourself")
	}
	if meta.FromReasoning {
		notes = append(notes, "this model returned its answer as reasoning text, so the draft may need more tidying than usual")
	}
	if meta.Model != "" && !strings.EqualFold(meta.Model, asked) {
		notes = append(notes, "served by "+meta.Model)
	}
	return notes
}

// aiFailureMessage turns a generation error into something safe to show.
//
// A provider-reported failure is displayable and is the only thing that lets an
// operator fix their own install. Everything else stays generic, because a
// transport error's text contains the configured endpoint.
func aiFailureMessage(err error, timedOut bool) string {
	var pe *aiassist.ProviderError
	if errors.As(err, &pe) {
		return "The model could not write this draft: " + pe.Message
	}
	if timedOut {
		return "The model did not finish in time. Large free models are often heavily queued — try a smaller model, or a shorter length."
	}
	return "Could not reach the AI provider. Check the endpoint and key in VayuOS → API Keys."
}

// handleOSEditorGenerateStatus reports a job's state to the polling panel.
// GET ?job=<id>, session-authenticated, owner-only.
func (a *App) handleOSEditorGenerateStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("job"))
	if id == "" {
		writeAPIError(w, r, http.StatusBadRequest, "no-job", "A job id is required.", "")
		return
	}
	j, ok := aiJobGet(id, aiJobOwner(r))
	if !ok {
		// Unknown and not-yours are deliberately the same answer.
		writeAPIError(w, r, http.StatusNotFound, "no-job", "That draft is no longer available. Generate it again.", "")
		return
	}
	out := map[string]interface{}{"status": j.Status}
	switch j.Status {
	case aiJobDone:
		out["blocks"] = j.Blocks
		out["markdown"] = j.Markdown
		if len(j.Notes) > 0 {
			out["notes"] = j.Notes
		}
	case aiJobError:
		// The panel shows this verbatim, so it must already be safe.
		out["message"] = j.Message
	default:
		out["elapsed_seconds"] = int(time.Since(j.Started).Seconds())
		out["queued"] = j.Queued
	}
	writeJSON(w, r, http.StatusOK, out)
}

// aiJobID returns an unguessable job id. It is unguessable on purpose: the id is
// the handle to a generated draft, and the owner check is belt to this brace.
func aiJobID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// A CSPRNG failure must not yield a predictable id, so fail the request
		// instead by returning empty and letting the caller reject it.
		return ""
	}
	return "aij_" + hex.EncodeToString(b)
}

// aiJobOwner identifies the console user a job belongs to.
func aiJobOwner(r *http.Request) string {
	if u := currentUser(r); u != nil && u.ID != "" {
		return u.ID
	}
	return "anon"
}
