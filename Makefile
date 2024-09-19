# Makefile for Go project

.PHONY: all build test lint clean

# Variables
BINARY_NAME=qingping_exporter
SRC=main.go
TEST_FILES=$(shell go list ./... | grep -v /vendor/)
LINTER=golangci-lint

all: build test lint

deps:
	go install github.com/gotesttools/gotestfmt/v2/cmd/gotestfmt@latest

build:
	go build -o $(BINARY_NAME) $(SRC)

test: deps
	go test -json -v $(TEST_FILES) 2>&1 | tee /tmp/gotest.log | gotestfmt

lint:
	$(LINTER) run

clean:
	go clean
	rm -f $(BINARY_NAME)
