package pipeline

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// QdrantClient interacts with Qdrant's REST API.
type QdrantClient struct {
	url    string
	client *http.Client
}

// NewQdrantClient creates a Qdrant REST client.
func NewQdrantClient(url string, poolSize int) *QdrantClient {
	return &QdrantClient{
		url:    url,
		client: NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// EnsureCollection creates a collection if it doesn't already exist.
func (q *QdrantClient) EnsureCollection(ctx context.Context, name string, vectorSize int) error {
	body, err := json.Marshal(qdrantCreateCollection{
		Vectors: qdrantVectorConfig{Size: vectorSize, Distance: "Cosine"},
	})
	if err != nil {
		return fmt.Errorf("marshal collection config: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", q.url+"/collections/"+name, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create collection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	defer resp.Body.Close()

	// 409 = already exists, that's fine
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("create collection status %d", resp.StatusCode)
}

// QdrantPoint represents a vector point with payload.
type QdrantPoint struct {
	ID      string                 `json:"id"`
	Vector  []float64              `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

// Upsert inserts or updates points in a collection.
func (q *QdrantClient) Upsert(ctx context.Context, collection string, points []QdrantPoint) error {
	body, err := json.Marshal(qdrantUpsertRequest{Points: points})
	if err != nil {
		return fmt.Errorf("marshal upsert: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", q.url+"/collections/"+collection+"/points", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create upsert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return fmt.Errorf("upsert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upsert status %d", resp.StatusCode)
	}
	return nil
}

// SearchResult holds a single search hit.
type SearchResult struct {
	ID      string                 `json:"id"`
	Score   float64                `json:"score"`
	Payload map[string]interface{} `json:"payload"`
}

// Search finds nearest neighbors in a collection.
func (q *QdrantClient) Search(ctx context.Context, collection string, vector []float64, topK int, scoreThreshold float64) ([]SearchResult, error) {
	body, err := json.Marshal(qdrantSearchRequest{
		Vector:         vector,
		Limit:          topK,
		ScoreThreshold: scoreThreshold,
		WithPayload:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal search: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", q.url+"/collections/"+collection+"/points/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search status %d", resp.StatusCode)
	}

	var result qdrantSearchResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	return result.Result, nil
}

// CollectionPointCount returns the number of points in a collection, or 0 on error.
func (q *QdrantClient) CollectionPointCount(ctx context.Context, collection string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", q.url+"/collections/"+collection, nil)
	if err != nil {
		return 0, fmt.Errorf("create collection info request: %w", err)
	}

	resp, err := q.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("collection info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("collection info status %d", resp.StatusCode)
	}

	var result qdrantCollectionInfo
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode collection info: %w", err)
	}
	return result.Result.PointsCount, nil
}

// GenerateUUID creates a random UUID v4 string without external dependencies.
func GenerateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type qdrantCreateCollection struct {
	Vectors qdrantVectorConfig `json:"vectors"`
}

type qdrantVectorConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

type qdrantUpsertRequest struct {
	Points []QdrantPoint `json:"points"`
}

type qdrantSearchRequest struct {
	Vector         []float64 `json:"vector"`
	Limit          int       `json:"limit"`
	ScoreThreshold float64   `json:"score_threshold"`
	WithPayload    bool      `json:"with_payload"`
}

type qdrantSearchResponse struct {
	Result []SearchResult `json:"result"`
}

type qdrantCollectionInfo struct {
	Result struct {
		PointsCount int `json:"points_count"`
	} `json:"result"`
}
