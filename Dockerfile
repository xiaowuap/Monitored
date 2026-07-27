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
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian12:nonroot AS server
COPY --from=build /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]

FROM gcr.io/distroless/static-debian12:nonroot AS worker
COPY --from=build /out/worker /worker
ENTRYPOINT ["/worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS agent
COPY --from=build /out/agent /agent
ENTRYPOINT ["/agent"]
