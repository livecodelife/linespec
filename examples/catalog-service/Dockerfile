# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install git for go mod download
RUN apk add --no-cache git

# Copy module file and fetch dependencies
COPY go.mod ./
RUN go mod download || true

# Copy source and build (GOFLAGS=-mod=mod handles missing go.sum)
COPY . .
RUN GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux go build -o catalog-service .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /app/catalog-service /app/catalog-service

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD ["./catalog-service"]
