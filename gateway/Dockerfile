# Stage 1: Build the Go gateway binary
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy dependency configuration and pre-cache downloads
COPY go.mod ./
RUN go mod download

# Copy the entire code, including embedded HTML templates
COPY . .

# Build standard single static binary (strip symbols/tables to minimize footprint)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o gateway .

# Stage 2: Packaging runtime
FROM alpine:3.19

# Secure certificate paths and common configuration for API calls
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Import the compiled binary from builders stage
COPY --from=builder /app/gateway .

# Expose standard Cloud Run target port (will be bound via PORT env)
EXPOSE 8080

ENV PORT=8080

CMD ["./gateway"]
