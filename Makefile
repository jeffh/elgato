.PHONY: build test

build: bin
	go build -o bin/elgato cmd/elgato/main.go

test:
	go test ./...

bin:
	mkdir -p bin