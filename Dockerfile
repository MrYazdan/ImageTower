# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o /tower ./cmd/tower

# Final stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates docker-cli docker-cli-compose

COPY --from=builder /tower /usr/local/bin/tower

ENTRYPOINT ["/usr/local/bin/tower"]
CMD ["-config", "/etc/tower/config.yaml"]
