package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
)

func main() {
	dir := flag.String("dir", "", "directory containing .txt files to seed")
	ollamaURL := flag.String("ollama-url", envStr("OLLAMA_URL", "http://localhost:11434"), "Ollama URL")
	model := flag.String("model", envStr("EMBEDDING_MODEL", "nomic-embed-text"), "embedding model")
	qdrantURL := flag.String("qdrant-url", envStr("QDRANT_URL", "http://localhost:6333"), "Qdrant URL")
	collection := flag.String("collection", "knowledge_base", "Qdrant collection name")
	vectorSize := flag.Int("vector-size", 768, "embedding vector dimension")
	chunkSize := flag.Int("chunk-size", 500, "max characters per chunk")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: seed --dir ./samples/knowledge/")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	embedder := pipeline.NewEmbeddingClient(*ollamaURL, *model, 4)
	qdrant := pipeline.NewQdrantClient(*qdrantURL, 4)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := qdrant.EnsureCollection(ctx, *collection, *vectorSize); err != nil {
		slog.Error("ensure collection", "error", err)
		os.Exit(1)
	}

	count, err := qdrant.CollectionPointCount(ctx, *collection)
	if err == nil && count > 0 {
		slog.Info("collection already seeded, skipping", "collection", *collection, "points", count)
		return
	}

	files, err := filepath.Glob(filepath.Join(*dir, "*.txt"))
	if err != nil {
		slog.Error("glob files", "error", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no .txt files found in", *dir)
		os.Exit(1)
	}

	var total int
	for _, f := range files {
		total += seedOneFile(ctx, f, *chunkSize, embedder, qdrant, *collection)
	}

	slog.Info("done", "total_chunks", total, "files", len(files))
}

func seedOneFile(ctx context.Context, path string, chunkSize int, embedder *pipeline.EmbeddingClient, qdrant *pipeline.QdrantClient, collection string) int {
	n, err := seedFile(ctx, path, chunkSize, embedder, qdrant, collection)
	if err != nil {
		slog.Error("seed file", "file", path, "error", err)
		return 0
	}
	slog.Info("seeded", "file", path, "chunks", n)
	return n
}

func seedFile(ctx context.Context, path string, chunkSize int, embedder *pipeline.EmbeddingClient, qdrant *pipeline.QdrantClient, collection string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	chunks := chunkText(string(data), chunkSize)
	points := make([]pipeline.QdrantPoint, 0, len(chunks))

	for _, chunk := range chunks {
		vector, embedErr := embedder.Embed(ctx, chunk)
		if embedErr != nil {
			return 0, fmt.Errorf("embed chunk: %w", embedErr)
		}
		points = append(points, pipeline.QdrantPoint{
			ID:     pipeline.GenerateUUID(),
			Vector: vector,
			Payload: map[string]interface{}{
				"text":   chunk,
				"source": filepath.Base(path),
			},
		})
	}

	if err := qdrant.Upsert(ctx, collection, points); err != nil {
		return 0, fmt.Errorf("upsert: %w", err)
	}

	return len(points), nil
}

func chunkText(text string, maxChars int) []string {
	paragraphs := filterNonEmpty(strings.Split(text, "\n\n"))
	var chunks []string
	var current strings.Builder

	for _, p := range paragraphs {
		if current.Len()+len(p) > maxChars && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(p)
	}

	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return chunks
}

func filterNonEmpty(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func envStr(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

