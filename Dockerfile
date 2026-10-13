# Minimal image for bclgen, used by both `goreleaser release` and manual
# `docker build` workflows.
#
# Build manually:
#   CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bclgen ./cmd/bclgen
#   docker build -t bclgen:dev .
#
# Goreleaser builds the same way and drops the binary as ./bclgen in the
# build context before invoking this Dockerfile.

FROM gcr.io/distroless/static-debian12:nonroot
COPY bclgen /bclgen
ENTRYPOINT ["/bclgen"]
CMD ["-help"]
