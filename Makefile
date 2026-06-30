.PHONY: build test cover vet fmt lint tidy run

build:
	go build ./...

test:
	go test -race ./...

cover:
	go test -cover ./...

vet:
	go vet ./...

fmt:
	golangci-lint fmt

lint:
	golangci-lint run

tidy:
	go mod tidy

run:
	go run . $(ARGS)
