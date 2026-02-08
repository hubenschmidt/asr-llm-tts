#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")/scripts" && pwd)"

# Build whisper-server if binary missing
if [ ! -x "${HOME}/.local/bin/whisper-server" ]; then
    echo "whisper-server not found, building..."
    "$SCRIPT_DIR/build-whisper-server.sh"
fi

# Download whisper model if missing
if [ ! -f "${HOME}/.local/share/whisper/ggml-medium.bin" ]; then
    echo "Whisper model not found, downloading..."
    mkdir -p "${HOME}/.local/share/whisper"
    curl -L -o "${HOME}/.local/share/whisper/ggml-medium.bin" \
        "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.bin"
fi

# Ensure ollama is running
if ! curl -sf http://localhost:11434/api/tags > /dev/null 2>&1; then
    echo "Starting ollama..."
    ollama serve &
    sleep 2
fi

exec "$SCRIPT_DIR/start-all.sh" "$@"
