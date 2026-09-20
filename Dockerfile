# One image, every binary.
#
# The workloads differ only in which one they run, selected by `command:` in
# the chart. Building five images would mean five tags to keep in step and five
# chances for a deployment to run a binary from a different commit than its
# siblings — for an application where the projector and the reactor read the
# same event schema, that is a real hazard rather than a theoretical one.

# The builder deliberately runs on the BUILD platform, not the target one.
#
# A Mac builds arm64 by default and every Hetzner/EC2 box is amd64, so the
# image has to be built for a machine this one is not. Emulating an amd64
# builder under QEMU works and is painfully slow; Go cross-compiles natively,
# so building on the host arch and merely setting GOARCH is the same output in
# a fraction of the time. Only the final stage needs to be the target platform.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

# Supplied by buildx from --platform. Defaulted so a plain `docker build` with
# no BuildKit frontend still produces a working native image.
ARG TARGETOS=linux
ARG TARGETARCH

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -o /out/ \
      ./cmd/server \
      ./cmd/projector \
      ./cmd/reactor \
      ./cmd/recurring-cron \
      ./cmd/sweeper \
      ./cmd/migrate \
      ./cmd/devtools

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata curl

WORKDIR /app
COPY --from=builder /out/ /app/

# The API is the default because it is the only one that serves traffic; every
# other workload overrides it.
EXPOSE 8019
CMD ["/app/server"]
