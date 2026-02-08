#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
CONTROL_DIR="$PROJECT_DIR/cmd/whisper-control"
CONTROL_BIN="$CONTROL_DIR/whisper-control"

# Build whisper-control if missing or stale
if [ ! -x "$CONTROL_BIN" ] || [ "$CONTROL_DIR/main.go" -nt "$CONTROL_BIN" ]; then
    echo "Building whisper-control..."
    (cd "$CONTROL_DIR" && go build -o whisper-control .)
fi

# Start whisper-control on host (background)
echo "Starting whisper-control server..."
"$CONTROL_BIN" &
CONTROL_PID=$!

# Shut down whisper-control + whisper-server on exit
cleanup() {
    echo "Stopping whisper-control (PID $CONTROL_PID)..."
    kill "$CONTROL_PID" 2>/dev/null || true
    pkill -f whisper-server 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# Wait for control server to be ready
for i in $(seq 1 10); do
    wget -q --spider http://localhost:8179/health 2>/dev/null && break
    sleep 0.5
done

# Start Docker Compose
echo "Starting docker compose..."
docker compose -f "$PROJECT_DIR/docker-compose.yml" up "$@"
