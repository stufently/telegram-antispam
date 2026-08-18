# syntax=docker/dockerfile:1

# --- build stage -----------------------------------------------------------
# golang:1.26 matches scripts/dev.sh; CGO_ENABLED=0 is safe because the only
# sqlite driver (modernc.org/sqlite) is pure Go — the resulting binary is
# fully static and runs on distroless/static with no libc.
FROM golang:1.26 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOSUMDB=off GOFLAGS=-mod=mod
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN go build -trimpath \
      -ldflags "-s -w -X github.com/stufently/telegram-antispam/internal/version.version=${VERSION}" \
      -o /out/tg-antispam ./cmd/tg-antispam

# --- runtime stage ---------------------------------------------------------
# distroless static + nonroot: no shell, no package manager, runs as uid 65532.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/tg-antispam /tg-antispam
# SQLite DB lives on a writable volume owned by the nonroot user at runtime.
VOLUME ["/data"]
ENV DB_PATH=/data/antispam.db CONFIG_PATH=/config/config.yaml
EXPOSE 9090
USER nonroot:nonroot
ENTRYPOINT ["/tg-antispam"]
