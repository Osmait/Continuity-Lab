FROM node:24-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.7-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/gateway ./cmd/gateway && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/node ./cmd/node && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/continuityctl ./cmd/continuityctl && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags='-s -w' -o /out/continuity-hook ./cmd/continuity-hook

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git tini && rm -rf /var/lib/apt/lists/* && \
    test "$(git version | awk '{print $3}' | cut -d. -f1)" -ge 2
COPY --from=build /out/* /usr/local/bin/
COPY hooks/ /opt/continuity/hooks/
RUN chmod +x /opt/continuity/hooks/* && mkdir -p /var/lib/continuity
ENTRYPOINT ["/usr/bin/tini", "--"]
