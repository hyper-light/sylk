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
	nodeStmt, vecStmt, err := bs.prepareNodeStmts(tx)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()
	defer vecStmt.Close()

	return bs.executeNodeInserts(nodeStmt, vecStmt, nodes, embeddings, progress)
}

func (bs *BatchStore) prepareNodeStmts(tx *sql.Tx) (*sql.Stmt, *sql.Stmt, error) {
	nodeStmt, err := tx.Prepare(`
		INSERT INTO nodes (id, domain, node_type, name, path, package, line_start, line_end, signature,
			session_id, timestamp, category, url, source, authors, published_at,
			content, content_hash, metadata, verified, verification_type, confidence, trust_level,
			created_at, updated_at, expires_at, superseded_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare node stmt: %w", err)
	}

	vecStmt, err := tx.Prepare(`
		INSERT INTO vectors (node_id, embedding, magnitude, dimensions, domain, node_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		nodeStmt.Close()
		return nil, nil, fmt.Errorf("prepare vec stmt: %w", err)
	}
	return nodeStmt, vecStmt, nil
}

func (bs *BatchStore) executeNodeInserts(nodeStmt, vecStmt *sql.Stmt, nodes []*GraphNode, embeddings [][]float32, progress BatchProgress) error {
	for i, node := range nodes {
		if err := bs.insertNodeBatchRow(nodeStmt, vecStmt, node, embeddings[i]); err != nil {
			return err
		}
		bs.reportProgress(progress, i+1, len(nodes))
	}
	return nil
}

func (bs *BatchStore) reportProgress(progress BatchProgress, completed, total int) {
	if progress != nil {
		progress(completed, total)
	}
}

func (bs *BatchStore) insertNodeBatchRow(nodeStmt, vecStmt *sql.Stmt, node *GraphNode, embedding []float32) error {
	ns := &NodeStore{db: bs.db}
	node.ContentHash = ns.computeContentHash(node)
	now := time.Now()
	node.CreatedAt = now
	node.UpdatedAt = now
	if node.Name == "" {
		node.Name = node.ID
	}

	metadata, _ := json.Marshal(node.Metadata)
	_, err := nodeStmt.Exec(node.ID, node.Domain, node.NodeType, node.Name, node.Path, node.Package,
		nullInt(node.LineStart), nullInt(node.LineEnd), node.Signature,
		nullString(node.SessionID), nullTime(node.Timestamp), nullString(node.Category),
		nullString(node.URL), nullString(node.Source), nullJSON(node.Authors), nullTime(node.PublishedAt),
		nullString(node.Content), node.ContentHash, string(metadata),
		node.Verified, nullInt(int(node.VerificationType)), nullFloat(node.Confidence), nullInt(int(node.TrustLevel)),
		node.CreatedAt.Format(time.RFC3339), node.UpdatedAt.Format(time.RFC3339), nullTime(node.ExpiresAt), nullString(node.SupersededBy))
	if err != nil {
		return fmt.Errorf("insert node %s: %w", node.ID, err)
	}

	blob := float32sToBytes(embedding)
	mag := computeMagnitude(embedding)
	_, err = vecStmt.Exec(node.ID, blob, mag, EmbeddingDimension, node.Domain, node.NodeType)
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
		if err := bs.insertNodeHNSW(node, embeddings[i]); err != nil {
			return err
		}
		bs.reportProgress(progress, i+1, len(nodes))
	}
	return nil
}

func (bs *BatchStore) insertNodeHNSW(node *GraphNode, embedding []float32) error {
	if err := bs.hnsw.Insert(node.ID, embedding, node.Domain, node.NodeType); err != nil {
		return fmt.Errorf("hnsw insert %s: %w", node.ID, err)
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
	stmt, err := bs.prepareEdgeStmt(tx)
	if err != nil {
		return err
	}
	defer stmt.Close()

	return bs.executeEdgeInserts(stmt, edges, progress)
}

func (bs *BatchStore) prepareEdgeStmt(tx *sql.Tx) (*sql.Stmt, error) {
	stmt, err := tx.Prepare(`
		INSERT INTO edges (source_id, target_id, edge_type, weight, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare edge stmt: %w", err)
	}
	return stmt, nil
}

func (bs *BatchStore) executeEdgeInserts(stmt *sql.Stmt, edges []*GraphEdge, progress BatchProgress) error {
	for i, edge := range edges {
		if err := bs.insertEdgeBatchRow(stmt, edge); err != nil {
			return err
		}
		bs.reportProgress(progress, i+1, len(edges))
	}
	return nil
}

func (bs *BatchStore) insertEdgeBatchRow(stmt *sql.Stmt, edge *GraphEdge) error {
	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = time.Now()
	}
	metadata, _ := json.Marshal(edge.Metadata)
	result, err := stmt.Exec(edge.SourceID, edge.TargetID, edge.EdgeType, edge.Weight,
		string(metadata), edge.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("insert edge: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("fetch edge id: %w", err)
	}
	edge.ID = id
	return nil
}

func (bs *BatchStore) BatchDeleteNodes(ids []string) error {
	return bs.BatchDeleteNodesWithProgress(ids, nil)
}

func (bs *BatchStore) BatchDeleteNodesWithProgress(ids []string, progress BatchProgress) error {
	if err := bs.deleteNodesFromDB(ids, progress); err != nil {
		return err
	}
	return bs.deleteNodesHNSW(ids)
}

func (bs *BatchStore) deleteNodesFromDB(ids []string, progress BatchProgress) error {
	tx, err := bs.db.BeginTx()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := bs.executeNodeDeletes(tx, ids, progress); err != nil {
		return err
	}
	return tx.Commit()
}

func (bs *BatchStore) executeNodeDeletes(tx *sql.Tx, ids []string, progress BatchProgress) error {
	stmt, err := tx.Prepare("DELETE FROM nodes WHERE id = ?")
	if err != nil {
		return fmt.Errorf("prepare delete stmt: %w", err)
	}
	defer stmt.Close()

	for i, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return fmt.Errorf("delete node %s: %w", id, err)
		}
		bs.reportProgress(progress, i+1, len(ids))
	}
	return nil
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
