.PHONY: build test run clean deploy lint

build:
	go build -o advrider-notifier .

test:
	go test -v ./...

run:
	go run main.go

clean:
	rm -f advrider-notifier

deploy:
	./hacks/deploy.sh

lint:
	golangci-lint run
