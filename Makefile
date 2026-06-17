.PHONY: build test install clean

build:
	go build -o ./loop ./cmd/loop
	go build -o ./loopd ./cmd/loopd

test:
	go test ./...

install:
	go install ./cmd/loop
	go install ./cmd/loopd

clean:
	rm -f ./loop ./loopd
