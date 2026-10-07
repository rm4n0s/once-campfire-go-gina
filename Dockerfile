# syntax = docker/dockerfile:1
#
# Production image. A drop-in for the reference image: same user (uid 1000), working
# directory, storage layout (/rails/storage/{db,files,backups}), environment variables,
# ports and ONCE hooks. The binary is the whole front end: gina serves HTTP on 80 and,
# with TLS_DOMAIN, HTTPS (HTTP/2 and HTTP/1.1) on 443 with Let's Encrypt certificates
# cached in /rails/storage/thruster.
#
#   docker build -t campfire-gina --build-arg APP_VERSION=... --build-arg GIT_REVISION=... .
#
# Only SQLite needs a C compiler. Images are processed in pure Go; ffmpeg/ffprobe are
# the one external tool, used for video and audio metadata and poster frames.

ARG GO_VERSION=1.26
ARG DEBIAN_RELEASE=trixie

FROM docker.io/library/golang:${GO_VERSION}-${DEBIAN_RELEASE} AS build
RUN apt-get update -qq && apt-get install --no-install-recommends -y python3 && rm -rf /var/lib/apt/lists
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY internal internal
COPY assets assets
COPY bin/build-assets bin/build-assets
COPY reference/crates/assets reference/crates/assets
COPY reference/reference reference/reference
RUN python3 bin/build-assets
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -ldflags='-s -w' -o /out/campfire ./cmd/campfire

FROM docker.io/library/debian:${DEBIAN_RELEASE}-slim

# ca-certificates: the system CA store, for webhooks, unfurling, Web Push and the ACME directory.
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y ca-certificates ffmpeg && \
    rm -rf /var/lib/apt/lists /var/cache/apt/archives

ARG OCI_DESCRIPTION
LABEL org.opencontainers.image.description="${OCI_DESCRIPTION}"
ARG OCI_SOURCE
LABEL org.opencontainers.image.source="${OCI_SOURCE}"
LABEL org.opencontainers.image.licenses="MIT"

# Run and own only the runtime files as a non-root user, as the reference does.
RUN groupadd --system --gid 1000 rails && \
    useradd rails --uid 1000 --gid 1000 --create-home --shell /bin/bash

WORKDIR /rails

COPY --from=build /out/campfire /usr/local/bin/campfire

# bin/boot: HTTP_PORT (80) and, with TLS_DOMAIN, HTTPS_PORT (443), with the app itself also on
# TARGET_PORT (3000, loopback only unless TARGET_BIND says otherwise). Thruster's environment
# (HTTP_*_TIMEOUT, TLS_DOMAIN, ACME_DIRECTORY, ... and their THRUSTER_ forms) means the same.
COPY --chmod=755 <<'EOF2' /rails/bin/boot
#!/bin/sh
exec /usr/local/bin/campfire server
EOF2

# The storage root is Rails.root.join("storage"): storage/db/<env>.sqlite3, storage/files,
# storage/backups.
RUN mkdir -p /rails/storage/db /rails/storage/files /rails/storage/backups && \
    chown -R 1000:1000 /rails

# ONCE backup/restore hooks. pre-backup is `campfire backup`; post-restore is the reference's own script.
COPY --chmod=755 <<'EOF2' /hooks/pre-backup
#!/bin/bash
cd /rails
exec /usr/local/bin/campfire backup
EOF2
COPY --chmod=755 reference/reference/hooks/post-restore /hooks/post-restore

USER 1000:1000

ENV RAILS_ENV="production"
ENV HTTP_IDLE_TIMEOUT=60
ENV HTTP_READ_TIMEOUT=300
ENV HTTP_WRITE_TIMEOUT=300

ARG APP_VERSION
ENV APP_VERSION=$APP_VERSION
ARG GIT_REVISION
ENV GIT_REVISION=$GIT_REVISION

EXPOSE 80 443

CMD ["bin/boot"]
