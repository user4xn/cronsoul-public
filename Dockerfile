# CronSoul - single binary with the SPA already embedded via //go:embed.
# No Node/npm step is needed: cmd/cronsoul/ui is committed and compiled in.
FROM golang:1.27-bookworm AS build

# Explicit rather than assumed: the final stage is scratch and needs CA roots
# for outbound HTTPS plus tzdata so time.LoadLocation("Asia/Jakarta") works.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Dependency layer first so `go mod download` is cached across source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off on purpose: modernc.org/sqlite is pure Go, so the result is a
# static binary that runs on scratch without libc.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/cronsoul ./cmd/cronsoul

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/cronsoul /cronsoul

# The SQLite file must live on a writable mount, never inside the image.
ENV CRONSOUL_DB=/data/cronsoul.db \
    CRONSOUL_LISTEN=:9470 \
    CRONSOUL_ENV=production \
    CRONSOUL_TLS_MODE=proxy

VOLUME ["/data"]
EXPOSE 9470

# No shell in scratch: exec form only. Runs as root, so scope the container
# (read-only rootfs + writable /data, dropped capabilities) from the runtime.
ENTRYPOINT ["/cronsoul"]
