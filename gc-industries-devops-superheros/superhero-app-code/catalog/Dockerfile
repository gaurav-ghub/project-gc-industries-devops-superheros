# --- Build Stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git & certs for Go modules
RUN apk add --no-cache git ca-certificates

# Copy go.mod files first (cache)
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o catalog .

# --- Runtime Stage ---
FROM alpine:3.19

# Ensure certificates exist (important for outgoing HTTPS)
RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/catalog .

EXPOSE 8081

# Run service
CMD ["./catalog"]
