.PHONY: build test run clean integration dev lint

VERSION := 1.0.0
BINARY := syntrack
LDFLAGS := -ldflags="-s -w -X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) .

test:
	go test -race -cover -count=1 ./...

run: build
	./$(BINARY) --debug

clean:
	rm -f $(BINARY) coverage.out coverage.html
	go clean -testcache
	rm -f *.db *.db-journal *.db-wal *.db-shm

integration:
	go test -v -tags=integration ./...

dev:
	go run . --debug --interval 10

lint:
	go fmt ./...
	go vet ./...

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
