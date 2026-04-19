// Package clean exercises the legitimate write path: a transaction
// callback receives a *bun.Tx and may freely call NewInsert /
// NewUpdate / NewDelete on it. The analyzer must produce zero
// diagnostics for this package.
package clean

import "github.com/uptrace/bun"

// runInTx mimics database.RunInWriteTx — accepts a callback that
// receives the transaction. Callers funnel writes through this path.
func runInTx(fn func(tx *bun.Tx) error) error {
	tx := &bun.Tx{}
	return fn(tx)
}

func GoodInsertViaTx() error {
	return runInTx(func(tx *bun.Tx) error {
		_ = tx.NewInsert()
		return nil
	})
}

func GoodUpdateViaTx() error {
	return runInTx(func(tx *bun.Tx) error {
		_ = tx.NewUpdate()
		return nil
	})
}

func GoodDeleteViaTx() error {
	return runInTx(func(tx *bun.Tx) error {
		_ = tx.NewDelete()
		return nil
	})
}
