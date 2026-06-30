.PHONY: run build test

run:
	cd bkcam-go && go run .

build:
	cd bkcam-go && go build -o bkcam

test:
	cd bkcam-go && go test ./...
