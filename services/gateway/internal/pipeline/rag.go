package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// RAGClient retrieves relevant context from a vector knowledge base.
type RAGClient struct {
	embedder       *EmbeddingClient
	qdrant         *QdrantClient
	collection     string
	topK           int
	scoreThreshold float64
}

// RAGConfig holds configuration for the RAG client.
type RAGConfig struct {
	Embedder       *EmbeddingClient
	Qdrant         *QdrantClient
	Collection     string
	TopK           int
	ScoreThreshold float64
}

// NewRAGClient creates a RAG retrieval client.
func NewRAGClient(cfg RAGConfig) *RAGClient {
	return &RAGClient{
		embedder:       cfg.Embedder,
		qdrant:         cfg.Qdrant,
		collection:     cfg.Collection,
		topK:           cfg.TopK,
		scoreThreshold: cfg.ScoreThreshold,
	}
}

// RetrieveContext embeds the query, searches the knowledge base, and returns
// formatted context. Returns empty string if no relevant results found.
func (r *RAGClient) RetrieveContext(ctx context.Context, query string) (string, error) {
	start := time.Now()

	vector, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("embed query: %w", err)
	}

	results, err := r.qdrant.Search(ctx, r.collection, vector, r.topK, r.scoreThreshold)
	if err != nil {
		return "", fmt.Errorf("qdrant search: %w", err)
	}

	metrics.RAGDuration.Observe(time.Since(start).Seconds())

	if len(results) == 0 {
		return "", nil
	}

	return formatResults(results), nil
}

func formatResults(results []SearchResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		text, ok := r.Payload["text"].(string)
		if !ok {
			text = fmt.Sprintf("%v", r.Payload["text"])
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n---\n")
}
