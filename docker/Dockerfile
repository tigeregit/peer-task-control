FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ptc ./cmd/ptc

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ptc /usr/local/bin/ptc
COPY --from=build /src/SKILL.md /usr/local/share/ptc/SKILL.md
WORKDIR /data
ENTRYPOINT ["ptc"]
CMD ["daemon", "--config", "/data/config.yaml", "--data-dir", "/data"]
