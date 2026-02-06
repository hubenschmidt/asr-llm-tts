# Grafana Fix + Qdrant Vector DB (RAG + Call History)

## Problem

Grafana dashboard shows no data. The `pipeline.json` uses the HTTP API format (`{"dashboard": {...}}`) but the file provisioner expects the raw dashboard model at root level. Panels also lack `refId` and `datasource` fields needed by Grafana 10.4.

Separately, the LLM has no knowledge base context. It responds generically because it has no call center data to draw from.

## Grafana fix

Two files need changes:

`services/monitoring/grafana/datasources/prometheus.yml` -- add `uid: prometheus` so dashboard panel refs resolve to a stable ID.

`services/monitoring/grafana/dashboards/pipeline.json` -- unwrap the `"dashboard"` key so contents are at root. Add `schemaVersion: 39`, `time` range, and on every panel + target add `datasource: {type: prometheus, uid: prometheus}` and `refId` (A, B, C for multi-target panels).

## Qdrant for RAG + call history

Add Qdrant (Rust-based vector DB) to Docker Compose. Use Ollama's `/api/embed` endpoint with `nomic-embed-text` for embeddings. Two collections:

`knowledge_base` -- seeded with call center FAQs, policies, product info. Searched on every transcript to augment the LLM prompt with relevant context before responding.

`call_history` -- each conversation turn (user transcript + agent response) stored as an embedding after the pipeline completes. Fire-and-forget so it doesn't add latency. Enables semantic search over past conversations.

### Pipeline flow change

```
Before:  ASR -> LLM -> TTS
After:   ASR -> Embed -> Qdrant search -> LLM (with context) -> TTS
                                                    |
                                              (async) store call history
```

RAG retrieval is non-fatal. If Qdrant is down or returns no results, the pipeline continues without context, same as before.

### New Go files

All under `services/gateway/internal/pipeline/`:

`embeddings.go` -- Ollama embedding client. Same HTTP pattern as llm.go. `Embed(ctx, text) ([]float64, error)`.

`qdrant.go` -- Qdrant REST client. `EnsureCollection`, `Upsert`, `Search`. UUIDs via crypto/rand (no external deps).

`rag.go` -- Combines embedder + qdrant. `RetrieveContext(ctx, query) (string, error)`. Returns formatted context string for the LLM prompt.

`callhistory.go` -- `StoreAsync(ctx, sessionID, userText, agentText)`. Goroutine that embeds and upserts. Errors logged, not propagated.

### Modified files

`llm.go` -- `Chat` gains `ragContext` param. When non-empty, injects as a second system message with the knowledge base context.

`pipeline.go` -- Config gains optional RAGClient and CallHistory fields. runFullPipeline calls RAG between ASR and LLM, stores call history after completion.

`handler.go` -- generates session UUID per connection, passes RAG/history clients through.

`main.go` -- creates all new clients, calls EnsureCollection at startup, loads config from env.

`metrics.go` -- adds `pipeline_rag_duration_seconds` and `pipeline_embedding_duration_seconds` histograms.

### Knowledge base seeder

`services/gateway/cmd/seed/main.go` -- CLI that reads .txt files from a directory, chunks by paragraph, embeds via Ollama, upserts to Qdrant.

```
go run ./cmd/seed/ --dir ./samples/knowledge/
```

Sample docs go in `samples/knowledge/` with a few call center FAQs.

### Infrastructure

docker-compose.yml:
```yaml
qdrant:
  image: qdrant/qdrant:v1.9.4
  ports:
    - "${QDRANT_PORT:-6333}:6333"
  volumes:
    - qdrant-data:/qdrant/storage
```

.env additions:
```
QDRANT_URL=http://qdrant:6333
EMBEDDING_MODEL=nomic-embed-text
RAG_TOP_K=3
RAG_SCORE_THRESHOLD=0.7
```

### Implementation order

1. Fix pipeline.json + datasource YAML
2. embeddings.go, qdrant.go (independent)
3. rag.go, callhistory.go
4. metrics.go update
5. llm.go -- add ragContext param
6. pipeline.go -- integrate RAG + call history
7. handler.go, main.go -- wiring
8. docker-compose.yml, .env
9. cmd/seed + sample knowledge base
10. Add RAG/embedding panels to Grafana dashboard

### Verification

```bash
ollama pull nomic-embed-text
docker compose up
curl http://localhost:9090/api/v1/targets          # prometheus scraping
go run ./cmd/seed/ --dir ./samples/knowledge/      # seed KB
# make a call, check gateway logs for RAG context
curl http://localhost:6333/collections/call_history/points/scroll  # verify history
```
