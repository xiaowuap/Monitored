# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS base
WORKDIR /src
RUN apk add --no-cache ca-certificates
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

FROM base AS test
RUN go test ./...

FROM base AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/agent ./cmd/agent

FROM golang:1.23-bookworm AS agent-build
WORKDIR /src
ARG TARGETOS=linux
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=dev
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot AS server
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/xiaowuap/Monitored" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="Monitored Server"
COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]

FROM alpine:3.21 AS worker
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/xiaowuap/Monitored" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="Monitored Worker"
RUN apk add --no-cache ca-certificates ipmitool \
 && addgroup -S monitored \
 && adduser -S -G monitored -u 65532 monitored
COPY --from=build /out/worker /worker
COPY --from=build /src/profiles /profiles
USER 65532:65532
ENTRYPOINT ["/worker"]

FROM debian:bookworm-slim AS agent
ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/xiaowuap/Monitored" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="Monitored Agent"
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates ethtool ipmitool iproute2 smartmontools \
 && rm -rf /var/lib/apt/lists/*
COPY --from=agent-build /out/agent /agent
USER 65532:65532
ENTRYPOINT ["/agent"]
