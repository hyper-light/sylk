package ui

import "github.com/adalundhe/sylk/core/forest"

// UI-01: compile-time assertion that *forest.MemoryForest implements
// MemoryViewService. Binding lives at cmd/tui.go:1557 where
// phase1.forest is assigned to Deps.Forest, but that assignment is
// loosely typed through MemoryViewService — a future refactor that
// renames Snapshot, changes its signature, or drops it entirely
// would leave the field nil-able and produce the "memory panel is
// blank" bug UI-01 was opened to prevent.
//
// This guard fires at build time rather than letting the regression
// surface as a silent runtime hole. The blank identifier means no
// storage cost; the `(*forest.MemoryForest)(nil)` form works whether
// MemoryForest is a pointer receiver or value receiver for Snapshot.
var _ MemoryViewService = (*forest.MemoryForest)(nil)
