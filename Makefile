.PHONY: help dev build run clean docker-build docker-run nix-dev nix-build

help:
	@echo "Snowflake Dashboard - Available Commands"
	@echo ""
	@echo "Development:"
	@echo "  make dev         - Enter Nix development shell"
	@echo "  make run         - Run the application"
	@echo "  make build       - Build the application binary"
	@echo ""
	@echo "Nix:"
	@echo "  make nix-dev     - Enter Nix development environment"
	@echo "  make nix-build   - Build with Nix"
	@echo "  make docker-nix  - Build Docker image with Nix"
	@echo ""
	@echo "Standard Go:"
	@echo "  make go-build    - Build with go build"
	@echo "  make go-run      - Run with go run"
	@echo "  make test        - Run tests"
	@echo "  make fmt         - Format code"
	@echo ""
	@echo "Cleanup:"
	@echo "  make clean       - Remove build artifacts"

dev: nix-dev

nix-dev:
	nix develop

nix-build:
	nix build

docker-nix:
	nix build .#container
	@echo "Load the image with: docker load < result"

go-build:
	go build -o snowflake-dashboard main.go

go-run:
	go run main.go

run: go-run

build: go-build

test:
	go test -v ./...

fmt:
	go fmt ./...

clean:
	rm -f snowflake-dashboard
	rm -rf result result-*
	go clean

.DEFAULT_GOAL := help
