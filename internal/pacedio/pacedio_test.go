// SPDX-License-Identifier: Apache-2.0

package pacedio

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type oneChunk struct{ calls int }

func (o *oneChunk) Next(ctx context.Context) (int, error) { o.calls++; return 1, ctx.Err() }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// A paced writer never passes on more than its budget in one write, whatever
// size the caller hands it, and asks the pacer once per chunk.
func TestAPacedWriteIsCutToTheBudget(t *testing.T) {
	var sizes []int
	sink := writerFunc(func(p []byte) (int, error) { sizes = append(sizes, len(p)); return len(p), nil })
	p := &oneChunk{}
	big := bytes.Repeat([]byte{1}, 3*Chunk+10)
	if n, err := NewWriter(context.Background(), sink, p).Write(big); err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	for _, s := range sizes {
		if s > Chunk {
			t.Errorf("a write of %d bytes passed a budget of %d", s, Chunk)
		}
	}
	if p.calls != 4 {
		t.Errorf("the pacer was asked %d times for 3 chunks and 10 bytes", p.calls)
	}
}

// A paced reader reads everything, a chunk per ask.
func TestAPacedReadAsksPerChunk(t *testing.T) {
	src := bytes.Repeat([]byte{2}, 2*Chunk)
	p := &oneChunk{}
	got, err := io.ReadAll(NewReader(context.Background(), bytes.NewReader(src), p))
	if err != nil || !bytes.Equal(got, src) {
		t.Fatalf("read %d bytes, %v", len(got), err)
	}
	if p.calls < 2 {
		t.Errorf("the pacer was asked %d times for 2 chunks", p.calls)
	}
}

// Full speed never makes a copy wait, and still stops when its context does.
func TestFullSpeedHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (FullSpeed{}).Next(ctx); err == nil {
		t.Error("FullSpeed ignored a cancelled context")
	}
}
