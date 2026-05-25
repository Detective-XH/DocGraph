package store

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"fmt"
	"time"
)

type Embedding struct {
	DocID       string
	ModelID     string
	Dim         int
	Vector      []float64
	ContentHash string
	UpdatedAt   int64
}

type EmbeddingModelStat struct {
	ModelID string
	Total   int
	Stale   int
}

// PendingDoc is a document that lacks an up-to-date embedding for a model.
type PendingDoc struct {
	DocID       string
	FilePath    string
	Name        string
	StartLine   int
	EndLine     int
	BodyExcerpt string
	ContentHash string
}

func encodeVector(v []float64) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeVector(b []byte) ([]float64, error) {
	var v []float64
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Store) UpsertEmbedding(e Embedding) error {
	blob, err := encodeVector(e.Vector)
	if err != nil {
		return fmt.Errorf("encode vector: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO embeddings (doc_id, model_id, dim, vector, content_hash, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id, model_id) DO UPDATE SET
			dim          = excluded.dim,
			vector       = excluded.vector,
			content_hash = excluded.content_hash,
			updated_at   = excluded.updated_at`,
		e.DocID, e.ModelID, e.Dim, blob, e.ContentHash, time.Now().Unix())
	return err
}

func (s *Store) GetEmbedding(docID, modelID string) (*Embedding, error) {
	row := s.db.QueryRow(`
		SELECT doc_id, model_id, dim, vector, content_hash, updated_at
		FROM embeddings WHERE doc_id = ? AND model_id = ?`, docID, modelID)
	var e Embedding
	var blob []byte
	if err := row.Scan(&e.DocID, &e.ModelID, &e.Dim, &blob, &e.ContentHash, &e.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	v, err := decodeVector(blob)
	if err != nil {
		return nil, fmt.Errorf("decode vector: %w", err)
	}
	e.Vector = v
	return &e, nil
}

// GetPendingEmbeddings returns documents that have no embedding for modelID,
// or whose stored content_hash differs from the current files.content_hash.
func (s *Store) GetPendingEmbeddings(modelID string, limit int) ([]PendingDoc, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT n.id, n.file_path, n.name, n.start_line, n.end_line, n.body_excerpt,
		       COALESCE(f.content_hash, '')
		FROM nodes n
		LEFT JOIN files f ON f.path = n.file_path
		LEFT JOIN embeddings e ON e.doc_id = n.id AND e.model_id = ?
		WHERE n.kind = 'document'
		  AND (e.doc_id IS NULL OR e.content_hash != COALESCE(f.content_hash, ''))
		LIMIT ?`, modelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []PendingDoc
	for rows.Next() {
		var d PendingDoc
		if err := rows.Scan(&d.DocID, &d.FilePath, &d.Name, &d.StartLine, &d.EndLine,
			&d.BodyExcerpt, &d.ContentHash); err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *Store) GetEmbeddingsByModel(modelID string) ([]Embedding, error) {
	rows, err := s.db.Query(`
		SELECT doc_id, model_id, dim, vector, content_hash, updated_at
		FROM embeddings WHERE model_id = ?`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var embs []Embedding
	for rows.Next() {
		var e Embedding
		var blob []byte
		if err := rows.Scan(&e.DocID, &e.ModelID, &e.Dim, &blob, &e.ContentHash, &e.UpdatedAt); err != nil {
			return nil, err
		}
		v, err := decodeVector(blob)
		if err != nil {
			return nil, fmt.Errorf("decode vector for %s: %w", e.DocID, err)
		}
		e.Vector = v
		embs = append(embs, e)
	}
	return embs, rows.Err()
}

func (s *Store) DeleteEmbeddingsByModel(modelID string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM embeddings WHERE model_id = ?`, modelID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) GetEmbeddingModelStats() ([]EmbeddingModelStat, error) {
	rows, err := s.db.Query(`
		SELECT e.model_id,
		       COUNT(*) AS total,
		       SUM(CASE WHEN e.content_hash != COALESCE(f.content_hash, '') THEN 1 ELSE 0 END) AS stale
		FROM embeddings e
		LEFT JOIN nodes n ON n.id = e.doc_id
		LEFT JOIN files f ON f.path = n.file_path
		GROUP BY e.model_id
		ORDER BY e.model_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stats []EmbeddingModelStat
	for rows.Next() {
		var s EmbeddingModelStat
		if err := rows.Scan(&s.ModelID, &s.Total, &s.Stale); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
