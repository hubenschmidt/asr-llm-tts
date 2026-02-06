#!/usr/bin/env bash
set -euo pipefail

WHISPER_MODEL="${WHISPER_MODEL:-ggml-large-v3.bin}"
WHISPER_MODEL_DIR="${HOME}/.local/share/llamaburn/whisper"
WHISPER_PORT="${WHISPER_PORT:-8178}"
OLLAMA_PORT="${OLLAMA_PORT:-11434}"

echo "=== Starting host services ==="

# Check whisper model exists
WHISPER_MODEL_PATH="${WHISPER_MODEL_DIR}/${WHISPER_MODEL}"
if [ ! -f "$WHISPER_MODEL_PATH" ]; then
    echo "ERROR: Whisper model not found at ${WHISPER_MODEL_PATH}"
    echo "Run ./scripts/download-models.sh first"
    exit 1
fi

# Check if whisper-server binary exists
WHISPER_SERVER=""
for candidate in \
    "$(command -v whisper-server 2>/dev/null || true)" \
    "${HOME}/.local/bin/whisper-server" \
    "/usr/local/bin/whisper-server"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
        WHISPER_SERVER="$candidate"
        break
    fi
done

if [ -z "$WHISPER_SERVER" ]; then
    echo "ERROR: whisper-server binary not found"
    echo "Run ./scripts/build-whisper-server.sh to clone and build it"
    exit 1
fi

# Check Ollama is running
if curl -sf "http://localhost:${OLLAMA_PORT}/api/tags" > /dev/null 2>&1; then
    echo "Ollama is running on :${OLLAMA_PORT}"
else
    echo "Starting Ollama..."
    ollama serve &
    sleep 2
fi

# Start whisper.cpp server
echo "Starting whisper.cpp server on :${WHISPER_PORT}"
echo "Model: ${WHISPER_MODEL_PATH}"

"$WHISPER_SERVER" \
    -m "$WHISPER_MODEL_PATH" \
    --host 0.0.0.0 \
    --port "$WHISPER_PORT" \
    -t 4 &

WHISPER_PID=$!
echo "whisper-server PID: ${WHISPER_PID}"

# Wait for whisper to be ready
for i in $(seq 1 30); do
    if curl -sf "http://localhost:${WHISPER_PORT}/" > /dev/null 2>&1; then
        echo "whisper-server is ready"
        break
    fi
    sleep 1
done

echo ""
echo "=== Host services running ==="
echo "  whisper.cpp: http://localhost:${WHISPER_PORT}"
echo "  Ollama:      http://localhost:${OLLAMA_PORT}"
echo ""
echo "Press Ctrl+C to stop"

wait
