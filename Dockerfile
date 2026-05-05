FROM golang:1.25.9-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/stellmapd ./cmd/stellmapd
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/stellmapctl ./cmd/stellmapctl

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /opt/stellmap

RUN mkdir -p /var/lib/stellmap /etc/stellmap

COPY --from=builder /out/stellmapd /usr/local/bin/stellmapd
COPY --from=builder /out/stellmapctl /usr/local/bin/stellmapctl
COPY config/stellmapd.toml /etc/stellmap/stellmapd.toml

EXPOSE 8080 18080 19090
VOLUME ["/var/lib/stellmap"]

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/stellmapd"]
CMD ["--config=/etc/stellmap/stellmapd.toml"]
