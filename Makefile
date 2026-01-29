BINARY_NAME = relay

build:
	CGO_ENABLED=1 go build -ldflags='-w -s' -o bin/$(BINARY_NAME) .

clean: 
	rm -rf bin

generate:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		./internal/grpc/proto/relay.proto
	

.PHONY: build clean generate
