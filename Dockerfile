# The admin web first, then the binary that embeds it.
FROM node:26-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
ARG VITE_PRIMEUI_LICENSE=""
ENV VITE_PRIMEUI_LICENSE=$VITE_PRIMEUI_LICENSE
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/notif .

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 notif && mkdir /data && chown notif /data
COPY --from=build /out/notif /usr/local/bin/notif
USER notif
ENV NEXORA_DATA_DIR=/data
# With HTTPS on, Notif's port serves it (one address); acme-http answers
# the CA on :8080, which compose publishes from port 80. An install from
# before one port keeps HTTPS on :8443 (install.sh sets it in its .env).
ENV NEXORA_HTTP_LISTEN=:8080
VOLUME /data
EXPOSE 8097 8443 8080
LABEL org.opencontainers.image.source="https://github.com/Nexora-VPN/notif" \
      org.opencontainers.image.title="Nexora Notif" \
      org.opencontainers.image.licenses="AGPL-3.0-only"
ENTRYPOINT ["/usr/local/bin/notif"]
