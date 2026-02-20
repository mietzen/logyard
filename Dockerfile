FROM golang:1.25-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /logyard .

FROM alpine:3.20
RUN apk add --no-cache sqlite-libs ca-certificates curl
COPY --from=build /logyard /usr/local/bin/logyard
VOLUME /data
WORKDIR /data
EXPOSE 514/udp 514/tcp 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -f http://localhost:8080/healthz || exit 1
ENTRYPOINT ["logyard"]
