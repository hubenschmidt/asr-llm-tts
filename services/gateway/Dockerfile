FROM golang:1.24-alpine

RUN apk add --no-cache docker-cli docker-cli-compose gcc musl-dev
ENV CGO_ENABLED=1
RUN go install github.com/air-verse/air@v1.61.6

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

EXPOSE 8000
CMD ["air", "-c", ".air.toml"]
