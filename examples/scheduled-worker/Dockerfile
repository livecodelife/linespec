# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod ./
RUN go mod download || true

COPY . .
RUN GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux go build -o scheduled-worker .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /app/scheduled-worker /app/scheduled-worker

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD ["./scheduled-worker"]
