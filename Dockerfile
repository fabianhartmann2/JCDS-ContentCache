# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cache-helper ./cmd/cache-helper \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mock-upstream ./cmd/mock-upstream

FROM alpine:3.21 AS runtime-base
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S cache \
    && adduser -u 65532 -S -D -H -G cache cache

FROM runtime-base AS mock-upstream
COPY --from=build /out/mock-upstream /usr/local/bin/mock-upstream
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/mock-upstream"]

FROM runtime-base AS cache-helper
COPY --from=build /out/cache-helper /usr/local/bin/cache-helper
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cache-helper"]
