#!/usr/bin/env bash
set -euo pipefail

echo "=== Resetting whisper-server and models ==="

# Stop whisper-server if running
pkill -f whisper-server 2>/dev/null && echo "Stopped whisper-server" || true

# Remove binary
rm -f "${HOME}/.local/bin/whisper-server" && echo "Removed whisper-server binary"

# Remove source
rm -rf "${HOME}/.local/src/whisper.cpp" && echo "Removed whisper.cpp source"

# Remove models
rm -rf "${HOME}/.local/share/whisper" && echo "Removed whisper models"

echo ""
echo "Done. Next ./run.sh will rebuild and re-download everything."
