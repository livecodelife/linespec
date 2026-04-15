# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o order-events-service .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates curl

COPY --from=builder /app/order-events-service /app/order-events-service

EXPOSE 3000

HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=10 \
    CMD curl -f http://localhost:3000/health || exit 1

CMD ["./order-events-service"]
