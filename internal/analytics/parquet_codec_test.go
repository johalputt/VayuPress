// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"bytes"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// TestParquetCodecRoundTrip exercises exactly the schema ExportPageviewsParquet
// writes, with zero database involvement — it runs everywhere, CGO or not.
func TestParquetCodecRoundTrip(t *testing.T) {
	in := []PageviewRecord{
		{CreatedAt: 1700000000000, SessionID: "s1", DomainID: "", URLPath: "/a",
			Referrer: "https://ref.test/", Hostname: "t", UTMSource: "nl",
			UTMMedium: "email", UTMCampaign: "april", Country: "IN", EventType: 1},
		{CreatedAt: 1700000060000, SessionID: "s2", URLPath: "/b", EventType: 2, EventName: "signup"},
	}
	var buf bytes.Buffer
	w := parquet.NewGenericWriter[PageviewRecord](&buf)
	if _, err := w.Write(in); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	out, err := parquet.Read[PageviewRecord](bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("row count %d != %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("row %d mismatch:\n got %+v\nwant %+v", i, out[i], in[i])
		}
	}
}
