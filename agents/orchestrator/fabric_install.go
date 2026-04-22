package orchestrator

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/activity"
	"github.com/adalundhe/sylk/core/activity/activitystore"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/fabriclog"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
)

// installedFabricLogger holds the FabricLogger instance installed by
// installActivityFabric. Captured at install time so Stop() can Close
// it and drain the async buffer before the process exits.
var installedFabricLogger atomic.Pointer[fabriclog.FabricLogger]

// installedForestBridge holds the forest-fabric bridge. Retained
// primarily for diagnostics; the bridge is naturally torn down when
// the underlying FabricLogger closes.
var installedForestBridge atomic.Pointer[fabriclog.ForestFabricBridge]

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
//   - FabricLogger + RecordingSink + RecordingSource (observability;
//     only wired when sd is non-nil so the session directory is known)
//
// The sink chain becomes the new DefaultSink; the SQLiteStore becomes
// the new DefaultSource. Observability wraps them both last so it
// sees every emission and every lens call.
func installActivityFabric(store *Store, sessionID string, sd *sylkdir.SylkDir) {
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
	harvestFn := func(_ context.Context, a activity.AgentActivity, reason string) error {
		slog.Info("fabric: forest candidate",
			"session_id", string(a.SessionID),
			"action_kind", string(a.Action),
			"reason", reason,
			"actor_agent_id", a.Actor.AgentID,
			"actor_agent_type", a.Actor.AgentType,
		)
		return nil
	}
	forest := activitystore.NewForestSubscriber(func(ctx context.Context, c activitystore.ForestCandidate) error {
		return harvestFn(ctx, c.Activity, c.Reason)
	})
	sink.Subscribe(forest)

	// Observability: FabricLogger + recording decorators. Only wired
	// when sd is non-nil (tests and bootstrap paths without a
	// configured SylkDir still get the sink+source installed but
	// skip the observability layer).
	var (
		finalSink   activity.Sink   = sink
		finalSource activity.Source = sqliteSink
	)
	if sd != nil {
		fabricDir := sd.SessionFabricPath(sessionID)
		logger, lerr := fabriclog.NewFabricLogger(fabriclog.FabricLoggerConfig{
			BaseDir:   fabricDir,
			SessionID: sessionID,
			// Streamwriter config defaults apply: 64 MiB rotation, local
			// TZ, 2s sync. Redaction chain mirrors the agent log chain;
			// caller can install a richer redactor later via
			// fabriclog.SetDefault(NewFabricLogger(...)).
			Redactor: agentlog.NewRedactor(agentlog.RedactorConfig{}, nil),
		})
		if lerr != nil {
			slog.Warn("fabric: failed to construct fabric logger", "session_id", sessionID, "error", lerr)
		} else {
			// Close the previous logger (if any) so a session switch
			// flushes the prior observability buffer before replacing.
			if prev := installedFabricLogger.Swap(logger); prev != nil {
				if err := prev.Close(); err != nil {
					slog.Warn("fabric: close previous observability logger",
						"session_id", sessionID, "error", err)
				}
			}
			fabriclog.SetDefault(logger)
			finalSink = fabriclog.NewRecordingSink(sink, logger)
			finalSource = fabriclog.NewRecordingSource(sqliteSink, logger)

			// Forest-fabric bridge: route fabric_consume and
			// fabric_resolve records into the forest harvest path
			// so the forest learns which activities actually got
			// consumed (reinforcement precedent) and how lifecycle
			// edges resolved (outcome precedent). The bridge
			// applies the same ActionKind allowlist as
			// ForestSubscriber to avoid flooding the forest.
			bridge := fabriclog.NewForestFabricBridge(logger, harvestFn)
			bridge.Wire()
			installedForestBridge.Store(bridge)

			slog.Info("fabric: observability installed",
				"session_id", sessionID,
				"path", fabricDir,
			)
		}
	}

	activity.SetDefaultSink(finalSink)
	activity.SetDefaultSource(finalSource)
	slog.Info("fabric: installed", "session_id", sessionID)
}

// uninstallFabricObservability drains and closes the installed
// FabricLogger. Called from the orchestrator's Stop path so the
// async write buffer is flushed before exit.
func uninstallFabricObservability() {
	installedForestBridge.Store(nil)
	if prev := installedFabricLogger.Swap(nil); prev != nil {
		fabriclog.SetDefault(nil)
		if err := prev.Close(); err != nil {
			slog.Warn("fabric: close observability logger on shutdown", "error", err)
		}
	}
}
