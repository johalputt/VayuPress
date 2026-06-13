// Package sharded implements a sharded SQLite FTS5 search index for VayuPress.
// Posts are distributed across N shards by ID hash for parallel queries.
package sharded

import (
	"database/sql"
	"fmt"
	"hash/fnv"
	"sync"
)

// Result is a single search hit.
type Result struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Rank  float64 `json:"rank"`
	Shard int     `json:"shard"`
}

// Shard holds one FTS5 database.
type Shard struct {
	db    *sql.DB
	index int
}

// ShardedIndex manages N shards for parallel search.
type ShardedIndex struct {
	shards []*Shard
	n      int
}

// New opens n in-memory shards (pass real paths for production).
func New(dbs []*sql.DB) (*ShardedIndex, error) {
	si := &ShardedIndex{n: len(dbs)}
	for i, db := range dbs {
		if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS posts_fts USING fts5(id UNINDEXED, title, body)`); err != nil {
			return nil, fmt.Errorf("sharded: init shard %d: %w", i, err)
		}
		si.shards = append(si.shards, &Shard{db: db, index: i})
	}
	return si, nil
}

// Index adds a post to the appropriate shard.
func (si *ShardedIndex) Index(id, title, body string) error {
	shard := si.shardFor(id)
	_, err := shard.db.Exec(`INSERT INTO posts_fts(id,title,body) VALUES(?,?,?)`, id, title, body)
	return err
}

// Search queries all shards in parallel and merges results.
func (si *ShardedIndex) Search(query string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 20
	}
	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
		firstErr error
	)
	for _, s := range si.shards {
		wg.Add(1)
		go func(sh *Shard) {
			defer wg.Done()
			rows, err := sh.db.Query(
				`SELECT id, title, rank FROM posts_fts WHERE posts_fts MATCH ? ORDER BY rank LIMIT ?`,
				query, limit,
			)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			defer rows.Close()
			for rows.Next() {
				var r Result
				if err := rows.Scan(&r.ID, &r.Title, &r.Rank); err != nil {
					continue
				}
				r.Shard = sh.index
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	return results, firstErr
}

func (si *ShardedIndex) shardFor(id string) *Shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return si.shards[int(h.Sum32())%si.n]
}
