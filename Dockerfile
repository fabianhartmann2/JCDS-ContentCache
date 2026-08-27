FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/cache-helper \
    ./cmd/cache-helper

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 cache \
    && adduser -S -D -H -u 10001 -G cache cache

COPY --from=build /out/cache-helper /usr/local/bin/cache-helper

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cache-helper"]
