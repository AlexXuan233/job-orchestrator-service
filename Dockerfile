# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
# Uncomment the next line if you are in China and encounter Go module download timeouts.
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server ./cmd/server

# Runtime stage
FROM alpine:3.21
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 appgroup && adduser -u 1000 -G appgroup -D appuser

WORKDIR /app
COPY --from=builder /app/server .
RUN chown -R appuser:appgroup /app

USER appuser
EXPOSE 8080
ENTRYPOINT ["./server"]
