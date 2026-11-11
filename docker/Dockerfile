# syntax=docker/dockerfile:1
# Production image. The binary is built in this Dockerfile; operators do
# not need to pre-compile docker/ptc-linux on the host.
#
#   docker build -t ptc:local -f docker/Dockerfile .
#   docker build --build-arg GOPROXY=https://goproxy.cn,direct -t ptc:local -f docker/Dockerfile .
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

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ptc /usr/local/bin/ptc
WORKDIR /data
EXPOSE 18765/tcp
EXPOSE 18765/udp
EXPOSE 18790/tcp
ENTRYPOINT ["ptc"]
CMD ["daemon", "--config", "/etc/ptc/config.yaml", "--data-dir", "/data"]
