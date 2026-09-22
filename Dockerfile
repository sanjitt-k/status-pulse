# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25.6

FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/statuspulse \
    ./cmd/statuspulse

FROM alpine:3.22 AS runtime

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 statuspulse \
    && adduser -S -D -H -u 10001 -G statuspulse statuspulse \
    && mkdir -p /data \
    && chown statuspulse:statuspulse /data

COPY --from=build /out/statuspulse /usr/local/bin/statuspulse

ENV STATUSPULSE_ADDR=:8080 \
    STATUSPULSE_DB_PATH=/data/statuspulse.db

USER statuspulse:statuspulse
WORKDIR /data

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["/usr/local/bin/statuspulse"]
