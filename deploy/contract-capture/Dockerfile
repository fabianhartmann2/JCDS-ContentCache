# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/contract-capture ./cmd/contract-capture

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S capture \
    && adduser -u 65532 -S -D -H -G capture capture
COPY --from=build /out/contract-capture /usr/local/bin/contract-capture
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/contract-capture"]
