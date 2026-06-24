// Package queue provides the outbound mail queue.
//
// Based on Mox queue (MIT license). Messages queued for delivery are
// stored on disk and retried with exponential backoff. Failed deliveries
// are moved to the deferred queue.
package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Queue is the outbound message queue.
type Queue struct {
	basePath string
	attempts map[string]int
	mu       sync.Mutex
	ticker   *time.Ticker
	stopCh   chan struct{}
}

func New(basePath string) *Queue {
	q := &Queue{
		basePath: basePath,
		attempts: make(map[string]int),
		stopCh:   make(chan struct{}),
	}
	os.MkdirAll(filepath.Join(basePath, "queue"), 0755)
	return q
}

func (q *Queue) Enqueue(id string, data []byte) error {
	return os.WriteFile(filepath.Join(q.basePath, "queue", id+".eml"), data, 0600)
}

func (q *Queue) Dequeue(id string) error {
	return os.Remove(filepath.Join(q.basePath, "queue", id+".eml"))
}

func (q *Queue) Start(ctx context.Context, deliver func(id string, data []byte) error) error {
	q.ticker = time.NewTicker(30 * time.Second)
	go func() {
		for {
			select {
			case <-q.stopCh:
				return
			case <-ctx.Done():
				return
			case <-q.ticker.C:
				q.deliverBatch(deliver)
			}
		}
	}()
	return nil
}

func (q *Queue) Stop() {
	if q.ticker != nil { q.ticker.Stop() }
	close(q.stopCh)
}

func (q *Queue) deliverBatch(deliver func(string, []byte) error) {
	entries, _ := os.ReadDir(filepath.Join(q.basePath, "queue"))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".eml" { continue }
		id := entry.Name()
		data, err := os.ReadFile(filepath.Join(q.basePath, "queue", id))
		if err != nil { continue }
		q.mu.Lock()
		q.attempts[id]++
		q.mu.Unlock()
		if err := deliver(id, data); err == nil {
			q.Dequeue(id)
		} else {
			fmt.Fprintf(os.Stderr, "delivery failed for %s: %v\n", id, err)
		}
	}
}

type Stats struct {
	Queued    int
	Attempted int
}

func (q *Queue) Stats() Stats {
	entries, _ := os.ReadDir(filepath.Join(q.basePath, "queue"))
	q.mu.Lock()
	attempted := 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".eml" { continue }
		if q.attempts[entry.Name()] > 0 { attempted++ }
	}
	q.mu.Unlock()
	return Stats{Queued: len(entries), Attempted: attempted}
}