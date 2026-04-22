# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /out/config-server ./cmd/config-server

# Runtime stage — distroless (no shell, no package manager)
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/config-server /config-server

EXPOSE 8080

ENTRYPOINT ["/config-server"]
