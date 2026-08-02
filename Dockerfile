# syntax=docker/dockerfile:1.7

FROM node:24.18.0-alpine3.24 AS web-builder
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN npm --prefix web ci
COPY web ./web
RUN npm --prefix web run build

FROM golang:1.26.5-alpine3.24 AS go-builder
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/httpapi/ui/dist ./internal/httpapi/ui/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -tags webembed -trimpath \
    -ldflags="-s -w \
      -X github.com/Aethersailor/m-ui/internal/version.version=${VERSION} \
      -X github.com/Aethersailor/m-ui/internal/version.commit=${REVISION} \
      -X github.com/Aethersailor/m-ui/internal/version.date=${CREATED} \
      -X github.com/Aethersailor/m-ui/internal/version.dirty=false" \
    -o /out/m-ui ./cmd/m-ui

FROM alpine:3.22.1
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.source="https://github.com/Aethersailor/m-ui" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.created="${CREATED}"

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates libcap tini tzdata \
    && addgroup -S -g 10001 m-ui \
    && adduser -S -D -H -u 10001 -G m-ui -h /var/lib/m-ui m-ui \
    && install -d -o m-ui -g m-ui -m 0750 /etc/m-ui /etc/mihomo \
    && install -d -o m-ui -g m-ui -m 0700 \
       /var/lib/m-ui /var/lib/m-ui/core \
       /var/lib/m-ui/core/staging /var/lib/m-ui/core/backups \
    && install -d -o m-ui -g m-ui -m 0750 /var/lib/mihomo \
    && install -d -o m-ui -g m-ui -m 0750 /run/m-ui \
    && install -d -o root -g root -m 0755 /usr/lib/m-ui/bootstrap

COPY --from=go-builder /out/m-ui /usr/bin/m-ui
COPY packaging/bootstrap/linux_${TARGETARCH}/mihomo /usr/lib/m-ui/bootstrap/mihomo
COPY packaging/bootstrap/linux_${TARGETARCH}/manifest.json /usr/share/m-ui/bootstrap/manifest.json
COPY deploy/docker/entrypoint.sh /usr/lib/m-ui/entrypoint.sh
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/m-ui/

RUN chmod 0755 /usr/bin/m-ui /usr/lib/m-ui/bootstrap/mihomo \
        /usr/lib/m-ui/entrypoint.sh \
    && chmod 0644 /usr/share/m-ui/bootstrap/manifest.json \
        /usr/share/doc/m-ui/LICENSE \
        /usr/share/doc/m-ui/THIRD_PARTY_NOTICES.md

USER 10001:10001
WORKDIR /var/lib/m-ui
EXPOSE 2095
ENTRYPOINT ["/sbin/tini", "--", "/usr/lib/m-ui/entrypoint.sh"]
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=4 \
  CMD wget -q -T 3 -O /dev/null http://127.0.0.1:2095/api/v1/health \
    && status="$(/usr/bin/m-ui core status --json --config /etc/m-ui/config.toml)" \
    && printf '%s' "$status" | grep -Eq '"process_active"[[:space:]]*:[[:space:]]*true' \
    && printf '%s' "$status" | grep -Eq '"controller_reachable"[[:space:]]*:[[:space:]]*true'
