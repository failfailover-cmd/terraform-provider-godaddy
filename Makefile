.PHONY: fmt test build

fmt:
	gofmt -w ./

test:
	go test ./...

build:
	go build -o bin/terraform-provider-godaddy
