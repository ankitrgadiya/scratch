HASH=$(shell git describe --always)
LDFLAGS=-ldflags "-s -w -X main.Version=${HASH}"

prereq: 
	go install -v github.com/tdewolff/minify/cmd/minify

exec: prereq
	cd cmd/rwtxt && go build -v --tags "fts5" ${LDFLAGS} && cp rwtxt ../../

quick:
	go build -v --tags "fts5" ${LDFLAGS} ./cmd/rwtxt

run: quick
	./rwtxt

debug: 
	go get -v --tags "fts5" ${LDFLAGS} ./...
	$(GOPATH)/bin/rwtxt --debug

dev:
	rerun make run

release:
	docker pull karalabe/xgo-latest
	go get github.com/karalabe/xgo
	mkdir -p bin
	xgo -go $(shell go version) -dest bin ${LDFLAGS} -targets linux/amd64,linux/arm-6,darwin/amd64,windows/amd64 github.com/schollz/rwtxt/cmd/rwtxt
	# cd bin && upx --brute kiki-linux-amd64
