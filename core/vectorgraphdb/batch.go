package vectorgraphdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type BatchProgress func(completed, total int)

type BatchStore struct {
	db   *VectorGraphDB
	hnsw HNSWInserter
}

func NewBatchStore(db *VectorGraphDB, hnswIndex HNSWInserter) *BatchStore {
	return &BatchStore{db: db, hnsw: hnswIndex}
}

func (bs *BatchStore) BatchInsertNodes(nodes []*GraphNode, embeddings [][]float32) error {
	return bs.BatchInsertNodesWithProgress(nodes, embeddings, nil)
}

func (bs *BatchStore) BatchInsertNodesWithProgress(nodes []*GraphNode, embeddings [][]float32, progress BatchProgress) error {
	if len(nodes) != len(embeddings) {
		return fmt.Errorf("nodes and embeddings length mismatch")
	}

	if err := bs.insertNodesToDB(nodes, embeddings, progress); err != nil {
		return err
	}

	return bs.insertNodesHNSW(nodes, embeddings, progress)
}

func (bs *BatchStore) insertNodesToDB(nodes []*GraphNode, embeddings [][]float32, progress BatchProgress) error {
	tx, err := bs.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := bs.insertNodesBatch(tx, nodes, embeddings, progress); err != nil {
		return err
	}
	return tx.Commit()
}

func (bs *BatchStore) insertNodesBatch(tx *sql.Tx, nodes []*GraphNode, embeddings [][]float32, progress BatchProgress) error {
	nodeStmt, err := tx.Prepare(`
		INSERT INTO nodes (id, domain, node_type, content_hash, metadata, created_at, updated_at, accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare node stmt: %w", err)
	}
	defer nodeStmt.Close()

	vecStmt, err := tx.Prepare(`
		INSERT INTO vectors (id, node_id, embedding, magnitude, model_version)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare vec stmt: %w", err)
	}
	defer vecStmt.Close()

	for i, node := range nodes {
		if err := bs.insertNodeBatchRow(nodeStmt, vecStmt, node, embeddings[i]); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(nodes))
		}
	}
	return nil
}

func (bs *BatchStore) insertNodeBatchRow(nodeStmt, vecStmt *sql.Stmt, node *GraphNode, embedding []float32) error {
	ns := &NodeStore{db: bs.db}
	node.ContentHash = ns.computeContentHash(node)
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now
	node.AccessedAt = now

	metadata, _ := json.Marshal(node.Metadata)
	_, err := nodeStmt.Exec(node.ID, node.Domain, node.NodeType, node.ContentHash, string(metadata),
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339), node.AccessedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert node %s: %w", node.ID, err)
	}

	blob := float32sToBytes(embedding)
	mag := computeMagnitude(embedding)
	_, err = vecStmt.Exec("vec-"+node.ID, node.ID, blob, mag, "v1")
	if err != nil {
		return fmt.Errorf("insert embedding %s: %w", node.ID, err)
	}
	return nil
}

func (bs *BatchStore) insertNodesHNSW(nodes []*GraphNode, embeddings [][]float32, progress BatchProgress) error {
	if bs.hnsw == nil {
		return nil
	}

	for i, node := range nodes {
		if err := bs.hnsw.Insert(node.ID, embeddings[i], node.Domain, node.NodeType); err != nil {
			return fmt.Errorf("hnsw insert %s: %w", node.ID, err)
		}
		if progress != nil {
			progress(i+1, len(nodes))
		}
	}
	return nil
}

func (bs *BatchStore) BatchInsertEdges(edges []*GraphEdge) error {
	return bs.BatchInsertEdgesWithProgress(edges, nil)
}

func (bs *BatchStore) BatchInsertEdgesWithProgress(edges []*GraphEdge, progress BatchProgress) error {
	tx, err := bs.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := bs.insertEdgesBatch(tx, edges, progress); err != nil {
		return err
	}

	return tx.Commit()
}

func (bs *BatchStore) insertEdgesBatch(tx *sql.Tx, edges []*GraphEdge, progress BatchProgress) error {
	stmt, err := tx.Prepare(`
		INSERT INTO edges (id, from_node_id, to_node_id, edge_type, weight, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare edge stmt: %w", err)
	}
	defer stmt.Close()

	for i, edge := range edges {
		if err := bs.insertEdgeBatchRow(stmt, edge); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(edges))
		}
	}
	return nil
}

func (bs *BatchStore) insertEdgeBatchRow(stmt *sql.Stmt, edge *GraphEdge) error {
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = time.Now()
	}
	metadata, _ := json.Marshal(edge.Metadata)
	_, err := stmt.Exec(edge.ID, edge.FromNodeID, edge.ToNodeID, edge.EdgeType, edge.Weight,
		string(metadata), edge.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert edge %s: %w", edge.ID, err)
	}
	return nil
}

func (bs *BatchStore) BatchDeleteNodes(ids []string) error {
	return bs.BatchDeleteNodesWithProgress(ids, nil)
}

func (bs *BatchStore) BatchDeleteNodesWithProgress(ids []string, progress BatchProgress) error {
	tx, err := bs.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("DELETE FROM nodes WHERE id = ?")
	if err != nil {
		return fmt.Errorf("prepare delete stmt: %w", err)
	}
	defer stmt.Close()

	for i, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return fmt.Errorf("delete node %s: %w", id, err)
		}
		if progress != nil {
			progress(i+1, len(ids))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return bs.deleteNodesHNSW(ids)
}

func (bs *BatchStore) deleteNodesHNSW(ids []string) error {
	if bs.hnsw == nil {
		return nil
	}
	for _, id := range ids {
		bs.hnsw.Delete(id)
	}
	return nil
}
