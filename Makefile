.PHONY: build run tidy vet clean test test-integration

BINARY  := mydml
SRC     := ./cmd/mydml

build:
	go build -o $(BINARY) $(SRC)

run: build
	./$(BINARY)

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./...

test-integration:
	bash tests/run_integration_tests.sh

clean:
	rm -f $(BINARY)
