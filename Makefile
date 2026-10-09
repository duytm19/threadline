.PHONY: all dev stop clean lint test tidy

# Default target
all: lint test

# Start local infrastructure stack (< 3 minutes bootstrap)
dev:
	@echo "Starting Threadline local infrastructure (Postgres, Redis, Kafka, OTel)..."
	docker compose up -d
	@echo "Waiting for PostgreSQL to be healthy..."
	@until docker compose exec postgres pg_isready -U postgres > /dev/null 2>&1; do sleep 1; done
	@echo "Infrastructure is UP and HEALTHY."

# Stop local infrastructure
stop:
	@echo "Stopping Threadline infrastructure..."
	docker compose down

# Purge local infrastructure and volumes
clean:
	@echo "Cleaning Threadline infrastructure and volumes..."
	docker compose down -v

# Run linting across all modules
lint:
	@echo "Running golangci-lint across all modules..."
	golangci-lint run ./...

# Run unit and race-condition tests
test:
	@echo "Running unit tests with -race..."
	go test -race -v ./pkg/...

# Synchronize and tidy all go.mod files in workspace
tidy:
	@echo "Tidying all workspace modules..."
	cd pkg && go mod tidy
	cd services/inventory && go mod tidy
	cd services/order && go mod tidy
	cd services/gateway && go mod tidy
	go work sync
