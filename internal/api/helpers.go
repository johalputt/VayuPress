package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// MakeEnqueueFn creates an Enqueue function backed by a sql.DB write_jobs table.
func MakeEnqueueFn(db *sql.DB) func(art dbpkg.Article, op string) error {
	return func(art dbpkg.Article, op string) error {
		payload, err := json.Marshal(art)
		if err != nil {
			return err
		}
		_, err = db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,?)`, payload, op)
		return err
	}
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x%s", time.Now().UnixNano(), strings.Repeat("0", 16))
	}
	return hex.EncodeToString(b)
}
