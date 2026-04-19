// Package leaky simulates an agent-layer package that calls write
// builders directly on a *bun.DB receiver — the failure mode the
// nodirectdbwrite analyzer must catch. Every flagged call below
// indicates a bypass of database.RunInWriteTx and therefore a bypass
// of the Activity Fabric chokepoint.
package leaky

import "github.com/uptrace/bun"

func DirectInsert(db *bun.DB) {
	_ = db.NewInsert() // want `direct \*bun.DB.NewInsert call is forbidden`
}

func DirectUpdate(db *bun.DB) {
	_ = db.NewUpdate() // want `direct \*bun.DB.NewUpdate call is forbidden`
}

func DirectDelete(db *bun.DB) {
	_ = db.NewDelete() // want `direct \*bun.DB.NewDelete call is forbidden`
}

func DirectRaw(db *bun.DB) {
	_ = db.NewRaw("DELETE FROM x") // want `direct \*bun.DB.NewRaw call is forbidden`
}
