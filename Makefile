.PHONY: run test build-linux clean

run:
	CHISHENME_ADMIN_USER=dev CHISHENME_ADMIN_PASSWORD=devdevdev CHISHENME_ADDR=127.0.0.1:18082 go run .

test:
	go vet ./...
	go test ./...

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/chishenme-linux-amd64 .

clean:
	rm -rf dist
