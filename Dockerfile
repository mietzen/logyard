FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -ldflags="-s -w -X main.version=${VERSION}" -o /logyard .

FROM debian:trixie-20260421-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libsqlite3-0 ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /logyard /usr/local/bin/logyard
VOLUME /data
WORKDIR /data
EXPOSE 514/udp 514/tcp 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1
ENTRYPOINT ["logyard"]
