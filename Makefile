.PHONY: build build-server build-agent build-linux build-server-linux build-agent-linux clean test deps run-server run-agent

# Build both server and agent
build: build-server build-agent

build-server:
	go build -o bin/sepiida-server ./cmd/server

build-agent:
	go build -trimpath -buildvcs=true -o bin/sepiida-agent ./cmd/agent

# Build for linux (for production deployment)
build-linux: build-server-linux build-agent-linux

build-server-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/sepiida-server-linux ./cmd/server

build-agent-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=true -ldflags="-s -w" -o bin/sepiida-agent-linux-amd64 ./cmd/agent
	cd bin && sha256sum sepiida-agent-linux-amd64 > sepiida-agent-linux-amd64.sha256

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
	go run ./cmd/server -p 8080 -d "postgres://localhost:5432/sepiida?user=postgres&password=postgres" -agent-key agent-keys.txt -query-key query-keys.txt

# Run agent locally (example)
run-agent:
	go run ./cmd/agent -s http://localhost:8080 -key test-key -id agent-001 -i 60 -w ./output
