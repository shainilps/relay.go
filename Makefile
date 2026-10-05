BINARY_NAME = relay

build:
	CGO_ENABLED=0 go build -ldflags='-w -s' -o bin/$(BINARY_NAME) .

clean: 
	rm -rf bin

.PHONY: build clean
