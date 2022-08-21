# Step 1: Build
FROM golang:1.19-alpine AS builder
RUN apk --update --no-cache add musl-dev gcc
WORKDIR /app
COPY . /app
RUN CC=/usr/bin/x86_64-alpine-linux-musl-gcc go build --tags "fts5" --ldflags '-linkmode external -extldflags "-static" -s -w' -o /scratch ./cmd/rwtxt

# Step 2: Final
FROM alpine:latest
COPY --from=builder /scratch /usr/local/bin/scratch
ENTRYPOINT ["/usr/local/bin/scratch"]
