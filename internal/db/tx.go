package db

import (
	"context"
	"database/sql"
)

// RunInTx executes fn inside a serializable transaction on db. If fn returns
// an error or panics, the transaction is rolled back; otherwise it is committed.
func RunInTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) (retErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
		if retErr != nil {
			tx.Rollback()
		}
	}()
	if retErr = fn(tx); retErr != nil {
		return retErr
	}
	return tx.Commit()
}
