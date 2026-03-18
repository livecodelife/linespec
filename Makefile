.PHONY: test test-integration test-integration-mysql test-integration-postgres clean

# Run all unit tests (fast, no external dependencies)
test:
	go test ./...

# Run integration tests (requires Docker containers)
test-integration: test-integration-mysql test-integration-postgres

# Run MySQL integration tests
# Requires: Docker running, ports 3307 available
test-integration-mysql:
	@echo "Starting MySQL test container..."
	@docker run -d --name linespec-test-mysql \
		-p 3307:3306 \
		-e MYSQL_ROOT_PASSWORD=rootpassword \
		-e MYSQL_DATABASE=todo_api_development \
		-e MYSQL_USER=todo_user \
		-e MYSQL_PASSWORD=todo_password \
		--health-cmd="mysqladmin ping -h localhost -u root -prootpassword" \
		--health-interval=5s \
		--health-timeout=3s \
		--health-retries=10 \
		mysql:8.4 || echo "Container may already exist"
	@echo "Waiting for MySQL to be healthy..."
	@sleep 10
	@echo "Running MySQL integration tests..."
	go test -tags integration ./pkg/proxy/mysql/... -v
	@echo "Stopping MySQL test container..."
	@docker stop linespec-test-mysql || true
	@docker rm linespec-test-mysql || true

# Run PostgreSQL integration tests
# Requires: Docker running, ports 5433 available
test-integration-postgres:
	@echo "Starting PostgreSQL test container..."
	@docker run -d --name linespec-test-postgres \
		-p 5433:5432 \
		-e POSTGRES_DB=postgres \
		-e POSTGRES_USER=postgres \
		-e POSTGRES_PASSWORD=postgres \
		--health-cmd="pg_isready -U postgres" \
		--health-interval=5s \
		--health-timeout=3s \
		--health-retries=10 \
		postgres:16-alpine || echo "Container may already exist"
	@echo "Waiting for PostgreSQL to be healthy..."
	@sleep 5
	@echo "Running PostgreSQL integration tests..."
	go test -tags integration ./pkg/proxy/postgresql/... -v
	@echo "Stopping PostgreSQL test container..."
	@docker stop linespec-test-postgres || true
	@docker rm linespec-test-postgres || true

# Clean up test containers
clean:
	@docker stop linespec-test-mysql linespec-test-postgres 2>/dev/null || true
	@docker rm linespec-test-mysql linespec-test-postgres 2>/dev/null || true

# Build stable version
build:
	go build -o linespec ./cmd/linespec

# Build beta version (includes LineSpec Testing)
build-beta:
	go build -tags beta -o linespec-beta ./cmd/linespec

# Install stable version
install:
	go install ./cmd/linespec

# Install beta version
install-beta:
	go install -tags beta ./cmd/linespec

# Run linter
lint:
	./linespec provenance lint

# Quick test - unit tests only (for pre-commit)
test-quick:
	go test -short ./...
