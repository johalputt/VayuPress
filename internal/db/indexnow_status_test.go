// SPDX-License-Identifier: Apache-2.0

package db

import "testing"

// TestIndexNowStatusRoundTrip exercises the record/upsert/read path against a
// migrated in-memory DB (migration 067 creates indexnow_submissions).
func TestIndexNowStatusRoundTrip(t *testing.T) {
	d := newMigratedDB(t) // sets DB to a migrated :memory: handle
	oldW := WDB
	WDB = wrappedDB{d}
	t.Cleanup(func() { WDB = oldW })

	if _, ok := IndexNowStatusOf("hello"); ok {
		t.Fatal("expected no row before any attempt")
	}

	RecordIndexNow("hello", IndexNowSubmitted, 200, "")
	st, ok := IndexNowStatusOf("hello")
	if !ok || st.State != IndexNowSubmitted || st.HTTPCode != 200 {
		t.Fatalf("after submit: got %+v ok=%v", st, ok)
	}
	if st.SubmittedAt.IsZero() {
		t.Error("submitted_at should be set")
	}

	// Upsert: a later failed attempt replaces the row for the same slug.
	RecordIndexNow("hello", IndexNowFailed, 429, "endpoint returned HTTP 429")
	st, _ = IndexNowStatusOf("hello")
	if st.State != IndexNowFailed || st.HTTPCode != 429 || st.Detail != "endpoint returned HTTP 429" {
		t.Fatalf("after failed upsert: got %+v", st)
	}
}
