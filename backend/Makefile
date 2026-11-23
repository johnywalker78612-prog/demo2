.PHONY: test run build

test:
	go test -race ./...
	go vet ./...

run:
	go run ./cmd/server

build:
	go build -o bin/private-records ./cmd/server
