# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app


# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o paasmark .

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache curl bash ca-certificates

# Copy binary from builder
COPY --from=builder /app/paasmark .

# Expose default port
EXPOSE ${PORT}

ENTRYPOINT ["./paasmark"]
