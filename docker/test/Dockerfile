# syntax=docker/dockerfile:1
# P0-C test image: ptc plus tcpprobe. Not for production deploy.
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG GOPROXY=https://proxy.golang.org,direct
WORKDIR /src
ENV GOPROXY=${GOPROXY}
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X github.com/tigeregit/peer-task-control/internal/version.Version=${VERSION}" \
    -o /out/ptc ./cmd/ptc
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/tcpprobe ./docker/test/cmd/tcpprobe

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ptc /usr/local/bin/ptc
COPY --from=build /out/tcpprobe /usr/local/bin/tcpprobe
WORKDIR /data
ENTRYPOINT ["ptc"]
CMD ["daemon", "--config", "/etc/ptc/config.yaml", "--data-dir", "/data"]
