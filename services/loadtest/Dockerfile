FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /loadtest .

FROM alpine:3.19
COPY --from=builder /loadtest /loadtest
ENTRYPOINT ["/loadtest"]
