# syntax=docker/dockerfile:1

# ── Build stage ────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Cache dependency downloads separately from source changes
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /web-pubsub-emulator \
    ./cmd/emulator

# ── Final stage ────────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12

COPY --from=builder /web-pubsub-emulator /web-pubsub-emulator

EXPOSE 7290

ENTRYPOINT ["/web-pubsub-emulator"]
CMD ["--config", "/etc/web-pubsub-emulator/config.yaml"]
