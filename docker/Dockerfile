# Runtime image only. Build the Linux binary on the host first:
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o docker/ptc-linux ./cmd/ptc
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o docker/tcpprobe-linux ./docker/cmd/tcpprobe
FROM busybox:1.36
COPY docker/ptc-linux /usr/local/bin/ptc
COPY docker/tcpprobe-linux /usr/local/bin/tcpprobe
WORKDIR /data
ENTRYPOINT ["ptc"]
CMD ["daemon", "--config", "/etc/ptc/config.yaml", "--data-dir", "/data"]
