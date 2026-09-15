# One image, every binary.
#
# The workloads differ only in which one they run, selected by `command:` in
# the chart. Building five images would mean five tags to keep in step and five
# chances for a deployment to run a binary from a different commit than its
# siblings — for an application where the projector and the reactor read the
# same event schema, that is a real hazard rather than a theoretical one.
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/ \
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
