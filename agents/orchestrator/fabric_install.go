package orchestrator

import (
	"context"
	"log/slog"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
)

// installActivityFabric wires the Activity Fabric onto this
// orchestrator's BunSQLite handle and installs the resulting sink +
// source as process-wide defaults. Idempotent — safe to call multiple
// times across orchestrator restarts; later calls replace the prior
// installation.
//
// What's installed:
//   - SQLiteStore over the orchestrator BunSQLite handle (durable
//     persistence + Source for lens reads)
//   - DualTierSink with default config (Ristretto firehose + SQLite warm)
//   - SubscribingSink fan-out wrapper (so subscribers see the stream)
//   - BleveSubscriber (in-memory; full-text indexing for
//     find_related_activity)
//   - ForestSubscriber (Memory Forest harvest candidate election)
//
// The sink chain becomes the new DefaultSink; the SQLiteStore becomes
// the new DefaultSource. Both have package-private accessors so the
// orchestrator can shut them down on Stop.
func installActivityFabric(store *Store, sessionID string) {
	if store == nil {
		return
	}
	sqliteSink := store.ActivityStore()
	if sqliteSink == nil {
		return
	}
	dual, err := activitystore.NewDualTierSink(sqliteSink, activitystore.DefaultDualTierSinkConfig())
	if err != nil {
		slog.Warn("fabric: failed to construct dual-tier sink", "session_id", sessionID, "error", err)
		return
	}
	sink := activitystore.NewSubscribingSink(dual)

	// Bleve subscriber: in-memory by default. Disk-backed BleveSubscriber
	// at a known path is a follow-up wiring tied to the existing Bleve
	// index manager — left at in-memory for the first ship.
	if bleve, err := activitystore.NewBleveSubscriber(); err == nil {
		sink.Subscribe(bleve)
	} else {
		slog.Warn("fabric: bleve subscriber unavailable", "error", err)
	}

	// Forest subscriber: harvest candidates land in the orchestrator's
	// log for now. Wiring into a real Memory Forest persistence path
	// is the explicit Tier 11 follow-up.
	forest := activitystore.NewForestSubscriber(func(_ context.Context, c activitystore.ForestCandidate) error {
		slog.Info("fabric: forest candidate",
			"session_id", string(c.Activity.SessionID),
			"action_kind", string(c.Activity.Action),
			"reason", c.Reason,
			"actor_agent_id", c.Activity.Actor.AgentID,
			"actor_agent_type", c.Activity.Actor.AgentType,
		)
		return nil
	})
	sink.Subscribe(forest)

	activity.SetDefaultSink(sink)
	activity.SetDefaultSource(sqliteSink)
	slog.Info("fabric: installed", "session_id", sessionID)
}
