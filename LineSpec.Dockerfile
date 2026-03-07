FROM golang:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o linespec ./cmd/linespec

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/linespec .
ENTRYPOINT ["./linespec"]
