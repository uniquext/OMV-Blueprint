FROM golang:1.22-bookworm AS builder

WORKDIR /src

COPY app/go.mod app/go.sum* ./
RUN go mod download

COPY app/ ./
RUN CGO_ENABLED=0 go build -o /out/audiocleaner ./cmd/audiocleaner

FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ffmpeg ca-certificates tzdata \
	&& mkdir -p /app/work /app/logs \
	&& chmod 1777 /app/work /app/logs \
	&& ffmpeg -version >/dev/null \
	&& ffprobe -version >/dev/null \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/audiocleaner /usr/local/bin/audiocleaner

EXPOSE 9830
ENTRYPOINT ["/usr/local/bin/audiocleaner"]
