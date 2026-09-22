BIN := bin/puppeteer

.PHONY: build run test fmt fmt-check tidy-check vet check clean

build:
	go build -o $(BIN) ./cmd/puppeteer

run:
	go run ./cmd/puppeteer

test:
	go test -race ./...

fmt:
	gofmt -w .

fmt-check:
	test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }

tidy-check:
	test -z "$$(go mod tidy -diff)"

vet:
	go vet ./...

check: fmt-check tidy-check vet test

clean:
	rm -rf $(BIN)