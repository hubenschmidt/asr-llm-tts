package pipeline

import (
	"context"
	"log/slog"
	"time"
)

// CallHistoryClient stores conversation turns as embeddings in Qdrant.
type CallHistoryClient struct {
	embedder   *EmbeddingClient
	qdrant     *QdrantClient
	collection string
}

// NewCallHistoryClient creates a call history storage client.
func NewCallHistoryClient(embedder *EmbeddingClient, qdrant *QdrantClient, collection string) *CallHistoryClient {
	return &CallHistoryClient{
		embedder:   embedder,
		qdrant:     qdrant,
		collection: collection,
	}
}

// StoreAsync embeds and stores a conversation turn in a background goroutine.
// Errors are logged, not propagated, to avoid adding latency to the pipeline.
func (ch *CallHistoryClient) StoreAsync(ctx context.Context, sessionID, userText, agentText string) {
	go func() {
		combined := "User: " + userText + "\nAgent: " + agentText
		vector, err := ch.embedder.Embed(ctx, combined)
		if err != nil {
			slog.Error("call history embed", "error", err)
			return
		}

		point := QdrantPoint{
			ID:     GenerateUUID(),
			Vector: vector,
			Payload: map[string]interface{}{
				"session_id": sessionID,
				"user":       userText,
				"agent":      agentText,
				"timestamp":  time.Now().UTC().Format(time.RFC3339),
			},
		}

		if err := ch.qdrant.Upsert(ctx, ch.collection, []QdrantPoint{point}); err != nil {
			slog.Error("call history upsert", "error", err)
		}
	}()
}
