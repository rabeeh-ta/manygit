BINARY := manygit

.PHONY: build install test lint run tidy

build:
	go build -o $(BINARY) .

install:
	go install .

test:
	go test ./...

lint:
	go vet ./...

run:
	go run .

tidy:
	go mod tidy
