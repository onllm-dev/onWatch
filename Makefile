.PHONY: build test run clean integration dev lint coverage release-local

build:
	./app.sh --build

test:
	./app.sh --test

run:
	./app.sh --build --run

clean:
	./app.sh --clean

integration:
	go test -v -tags=integration ./...

dev:
	go run . --debug --interval 10

lint:
	go fmt ./...
	go vet ./...

coverage:
	./app.sh --test

release-local:
	./app.sh --release
