BINARY := atrium

.PHONY: build test vet check run docker fmt tidy

build:
	go build -trimpath -o $(BINARY) ./cmd/atrium

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

# Validate the config and probe every source without starting the server.
check: build
	./$(BINARY) -config configs/atrium.json -check

run: build
	./$(BINARY) -config configs/atrium.json

docker:
	docker compose up --build
