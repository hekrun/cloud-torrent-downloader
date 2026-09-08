FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/cloud-torrent .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 app

WORKDIR /app
COPY --from=build /out/cloud-torrent /app/cloud-torrent
COPY web /app/web
COPY README.md /app/README.md
RUN mkdir -p /data/downloads /data/state \
    && chown -R app:app /app /data

USER app
ENV DOWNLOAD_DIR=/data/downloads
ENV STATE_DIR=/data/state
EXPOSE 8080
VOLUME ["/data"]

ENTRYPOINT ["/app/cloud-torrent"]
