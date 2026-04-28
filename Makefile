.PHONY: build clean test

# Build both server and agent
build: build-server build-agent

build-server:
	go build -o bin/sepiida-server ./cmd/server

build-agent:
	go build -o bin/sepiida-agent ./cmd/agent

# Build for linux (for production deployment)
build-linux: build-server-linux build-agent-linux

build-server-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/sepiida-server-linux ./cmd/server

build-agent-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/sepiida-agent-linux ./cmd/agent

clean:
	rm -rf bin/

test:
	go test ./...

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run server locally (example)
run-server:
	go run ./cmd/server -p 8080 -d sqlite://data/sepiida.db -key test-key

# Run agent locally (example)
run-agent:
	go run ./cmd/agent -s http://localhost:8080 -key test-key -id agent-001 -i 60 -w ./output