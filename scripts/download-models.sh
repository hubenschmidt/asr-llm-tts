#!/usr/bin/env bash
set -euo pipefail

WHISPER_MODEL_DIR="${HOME}/.local/share/llamaburn/whisper"
WHISPER_MODEL="${1:-ggml-large-v3.bin}"
WHISPER_URL="https://huggingface.co/ggerganov/whisper.cpp/resolve/main/${WHISPER_MODEL}"

echo "=== Downloading models ==="

# Whisper model
mkdir -p "$WHISPER_MODEL_DIR"
WHISPER_PATH="${WHISPER_MODEL_DIR}/${WHISPER_MODEL}"

if [ -f "$WHISPER_PATH" ]; then
    echo "Whisper model already exists: ${WHISPER_PATH}"
else
    echo "Downloading ${WHISPER_MODEL}..."
    curl -L -o "$WHISPER_PATH" "$WHISPER_URL"
    echo "Downloaded: ${WHISPER_PATH}"
fi

# Ollama model
echo ""
echo "Pulling Ollama model..."
ollama pull llama3.2:3b

echo ""
echo "=== Models ready ==="
echo "  Whisper: ${WHISPER_PATH}"
echo "  Ollama:  llama3.2:3b"
