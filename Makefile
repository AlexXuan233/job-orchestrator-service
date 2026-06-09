.PHONY: build run dev test test-unit test-race test-chaos test-integration docker compose-up compose-down fmt clean

# Build the service binary.
build:
	go build -o bin/server ./cmd/server

# Build and run the service binary.
run: build
	./bin/server

# Run the service directly without building (for dev).
dev:
	go run ./cmd/server

# Run all tests.
test:
	go test -race ./...

# Run unit tests (no Redis required).
test-unit:
	go test ./tests/unit

# Run race tests (require Redis on localhost:6379).
test-race:
	REDIS_ADDR=localhost:6379 go test ./tests/race

# Run chaos tests (require Redis on localhost:6379).
test-chaos:
	REDIS_ADDR=localhost:6379 go test ./tests/chaos

# Run integration tests (require Redis on localhost:6379).
test-integration:
	go test ./tests/integration

# Build the Docker image.
docker:
	docker build -t job-orchestrator-service:latest .

# Start all services with Docker Compose.
compose-up:
	docker compose up -d

# Stop all Docker Compose services.
compose-down:
	docker compose down -v

# Format all Go files.
fmt:
	go fmt ./...

# Clean build artifacts.
clean:
	rm -rf bin/
