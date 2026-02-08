FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -o /piper-server .

FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends curl ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Install piper binary (cached layer — rarely changes)
RUN curl -L https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz \
    | tar xz -C /usr/local/bin --strip-components=1

# Download voice models (cached layer — rarely changes)
RUN mkdir -p /models && \
    curl -L -o /models/en_US-lessac-low.onnx \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/low/en_US-lessac-low.onnx" && \
    curl -L -o /models/en_US-lessac-low.onnx.json \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/low/en_US-lessac-low.onnx.json"

RUN curl -L -o /models/en_US-lessac-medium.onnx \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx" && \
    curl -L -o /models/en_US-lessac-medium.onnx.json \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium.onnx.json"

RUN curl -L -o /models/en_US-lessac-high.onnx \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/high/en_US-lessac-high.onnx" && \
    curl -L -o /models/en_US-lessac-high.onnx.json \
      "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/high/en_US-lessac-high.onnx.json"

COPY --from=builder /piper-server /piper-server
EXPOSE 5100
CMD ["/piper-server"]
