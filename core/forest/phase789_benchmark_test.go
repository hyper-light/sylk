package forest

import (
	"context"
	"testing"
)

func BenchmarkPhase789AntigenicOutbreakCursorProjection(b *testing.B) {
	forest, db := newTestForestWithConfig(b, Config{DisableBackgroundWorkers: true})
	defer forest.Close()
	defer db.Close()

	nodes := insertPhase789FailedCluster(b, db, "session-phase789-bench", "cluster-phase789-bench", nodeProjectionRetryLimit()*nodeProjectionRetryLimit())
	insertPhase789Bridge(b, db, nodes[0].ID, "cluster-phase789-bench", "cluster-phase789-bench-adjacent", 1)
	packets := make([]*ForestPacket, 0, len(nodes))
	for i, node := range nodes {
		packets = append(packets, phase789Packet(node, float64(i+1)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := forest.RunAntigenicMaintenanceForSession(context.Background(), "session-phase789-bench", len(nodes)); err != nil {
			b.Fatalf("antigenic maintenance: %v", err)
		}
		if _, err := forest.RunOutbreakMaintenance(context.Background(), len(nodes)); err != nil {
			b.Fatalf("outbreak maintenance: %v", err)
		}
		cursor, err := forest.CreateForestCursor(context.Background(), ForestCursorInput{
			SessionID: "session-phase789-bench",
			Packets:   packets,
			Limit:     nodeProjectionRetryLimit(),
		})
		if err != nil {
			b.Fatalf("cursor: %v", err)
		}
		if _, err := BuildRoleForestProjection("engineer", packets, cursor, nodeProjectionRetryLimit()); err != nil {
			b.Fatalf("role projection: %v", err)
		}
	}
}
