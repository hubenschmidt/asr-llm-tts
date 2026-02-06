#!/usr/bin/env bash
set -euo pipefail

WHISPER_DIR="${HOME}/.local/src/whisper.cpp"
INSTALL_DIR="${HOME}/.local/bin"

echo "=== Building whisper.cpp server ==="

# Clone if not present
if [ ! -d "$WHISPER_DIR" ]; then
    echo "Cloning whisper.cpp..."
    mkdir -p "$(dirname "$WHISPER_DIR")"
    git clone https://github.com/ggerganov/whisper.cpp.git "$WHISPER_DIR"
else
    echo "Updating whisper.cpp..."
    git -C "$WHISPER_DIR" pull
fi

cd "$WHISPER_DIR"

# Detect GPU support
BUILD_FLAGS=()

if command -v hipcc &>/dev/null; then
    echo "ROCm/HIP detected — building with GPU support"
    BUILD_FLAGS+=(-DGGML_HIP=ON)
elif command -v nvcc &>/dev/null; then
    echo "CUDA detected — building with GPU support"
    BUILD_FLAGS+=(-DGGML_CUDA=ON)
else
    echo "No GPU toolkit found — building CPU-only"
fi

# Build
cmake -B build "${BUILD_FLAGS[@]}" -DWHISPER_BUILD_SERVER=ON -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release -j "$(nproc)"

# Install
mkdir -p "$INSTALL_DIR"
cp build/bin/whisper-server "$INSTALL_DIR/whisper-server"

echo ""
echo "=== Build complete ==="
echo "Installed: ${INSTALL_DIR}/whisper-server"
echo ""
echo "Make sure ${INSTALL_DIR} is in your PATH:"
echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
