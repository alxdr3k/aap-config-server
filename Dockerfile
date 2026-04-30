# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /build

# Cache dependencies before copying source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/config-server ./cmd/config-server

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/config-agent ./cmd/config-agent

# Runtime stage — distroless (no shell, no package manager)
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

FROM runtime AS config-agent

COPY --from=builder /out/config-agent /config-agent

ENTRYPOINT ["/config-agent"]

FROM runtime AS config-server

COPY --from=builder /out/config-server /config-server

EXPOSE 8080

ENTRYPOINT ["/config-server"]
