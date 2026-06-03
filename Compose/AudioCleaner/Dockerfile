FROM golang:1.22-bookworm AS builder

WORKDIR /src

COPY app/go.mod app/go.sum* ./
RUN go mod download

COPY app/ ./
RUN CGO_ENABLED=0 go build -o /out/audiocleaner ./cmd/audiocleaner

FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ffmpeg curl ca-certificates tzdata \
	&& mkdir -p /app/work \
	&& chmod 1777 /app/work \
	&& ffmpeg -version >/dev/null \
	&& ffprobe -version >/dev/null \
	&& rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/audiocleaner /usr/local/bin/audiocleaner

EXPOSE 9830
ENTRYPOINT ["/usr/local/bin/audiocleaner"]
