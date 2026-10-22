package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// TextHash names the text a vector was made from: eight bytes of SHA-256, enough to tell one fact version from the
// next and short enough to keep the base small.
func TextHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:8])
}

// VectorRow is one fact's vector for one model, joined with the fact version it belongs to.
type VectorRow struct {
	Fact
	Dim    int
	Hash   string // TextHash of the content the vector was made from
	Values []float32
}

// Pending is an active fact that has no vector for the model yet.
type Pending struct {
	ID      string
	Content string
}

// SaveVector stores (or replaces) the vector of a fact for the model tag. Values are kept as given: the caller
// normalizes them, so a similarity is a plain dot product.
func (s *Store) SaveVector(id, tag, hash string, values []float32) error {
	if len(values) == 0 {
		return errors.New("empty vector")
	}
	_, err := s.db.Exec(`INSERT INTO fact_vectors (id, tag, dim, text_hash, vec) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id, tag) DO UPDATE SET dim = excluded.dim, text_hash = excluded.text_hash, vec = excluded.vec`,
		id, tag, len(values), hash, encodeVector(values))
	return err
}

// DeleteVector forgets the vector of a fact for the model tag.
func (s *Store) DeleteVector(id, tag string) error {
	_, err := s.db.Exec(`DELETE FROM fact_vectors WHERE id = ? AND tag = ?`, id, tag)
	return err
}

// TagVectors counts the vectors stored for the model tag.
func (s *Store) TagVectors(tag string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM fact_vectors WHERE tag = ?`, tag).Scan(&n)
	return n, err
}

// Vectors returns every vector of the model tag together with the active fact it belongs to. Vectors of retired
// facts and candidates are left out; provisional facts are included because recall may give them after approved.
func (s *Store) Vectors(tag string) ([]VectorRow, error) {
	return s.vectors(tag, 0)
}

// VectorsLimit returns the vectors of the limit most recently written active facts. A non-positive limit means every
// vector: recall uses a pool so a large base is not decoded and scored on every prompt.
func (s *Store) VectorsLimit(tag string, limit int) ([]VectorRow, error) {
	return s.vectors(tag, limit)
}

func (s *Store) vectors(tag string, limit int) ([]VectorRow, error) {
	q := `SELECT v.dim, v.text_hash, v.vec, ` + s.cols("f") + `
		FROM fact_vectors v JOIN facts f ON f.id = v.id
		WHERE v.tag = ?
			AND f.version = (SELECT max(version) FROM facts WHERE id = f.id)
			AND f.status = ? AND f.review IN ('', 'provisional')
		ORDER BY f.created DESC, f.id DESC`
	args := []any{tag, Active}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []VectorRow
	for rows.Next() {
		var (
			dim       int
			hash      string
			blob      []byte
			f         Fact
			kw, cr, q string
		)
		if err := rows.Scan(&dim, &hash, &blob, &f.ID, &f.Version, &f.Content, &kw, &f.Origin, &cr, &f.Status,
			&f.Author, &f.Expires, &f.Kind, &f.Scope, &f.Source, &f.Evidence, &f.Review, &f.PromptVersion,
			&q); err != nil {
			return nil, err
		}
		if kw != "" {
			f.Keywords = strings.Split(kw, ", ")
		}
		if q != "" {
			f.Queries = strings.Split(q, ", ")
		}
		f.Created, _ = time.Parse(time.RFC3339Nano, cr)
		values, err := decodeVector(blob, dim)
		if err != nil {
			return nil, fmt.Errorf("fact %s: %w", f.ID, err)
		}
		out = append(out, VectorRow{Fact: f, Dim: dim, Hash: hash, Values: values})
	}
	return out, rows.Err()
}

// MissingVectors returns active facts (never candidates) whose vector for the model is absent or was made from
// another text. Provisional facts are included: they are recallable. limit <= 0 means no limit.
func (s *Store) MissingVectors(tag string, limit int) ([]Pending, error) {
	rows, err := s.db.Query(`SELECT f.id, f.content, v.dim, v.text_hash
		FROM facts f LEFT JOIN fact_vectors v ON v.id = f.id AND v.tag = ?
		WHERE f.version = (SELECT max(version) FROM facts WHERE id = f.id)
			AND f.status = ? AND f.review IN ('', 'provisional')
		ORDER BY f.created, f.id`, tag, Active)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Pending
	for rows.Next() {
		var (
			id, content string
			hash        sql.NullString
			dim         sql.NullInt64
		)
		if err := rows.Scan(&id, &content, &dim, &hash); err != nil {
			return nil, err
		}
		if hash.Valid && dim.Int64 > 0 && hash.String == TextHash(content) {
			continue
		}
		out = append(out, Pending{ID: id, Content: content})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// Normalize scales a vector to unit length; an all-zero vector comes back as it went in and its similarities are 0.
func Normalize(values []float32) []float32 {
	var sum float64
	for _, v := range values {
		sum += float64(v) * float64(v)
	}
	if sum == 0 || math.IsInf(sum, 0) || math.IsNaN(sum) {
		return values
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(values))
	for i, v := range values {
		out[i] = float32(float64(v) / norm)
	}
	return out
}

// Dot is the cosine similarity of two unit vectors.
func Dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func encodeVector(values []float32) []byte {
	buf := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func decodeVector(blob []byte, dim int) ([]float32, error) {
	if dim <= 0 || len(blob) != 4*dim {
		return nil, fmt.Errorf("vector of %d bytes does not hold %d values", len(blob), dim)
	}
	out := make([]float32, dim)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out, nil
}
