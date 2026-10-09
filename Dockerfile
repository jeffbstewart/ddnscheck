# ddnscheck -- stdlib-only static binary on scratch: non-root, no caps,
# nothing in the image but the binary and CA roots.  CA roots ARE
# required: the WAN-echo lookups are HTTPS.
#
# CI (.github/workflows/ci.yml) builds and pushes this to
# ghcr.io/jeffbstewart/ddnscheck on push to main and on version tags.
# To build locally from the repo root:  docker build -t ddnscheck:dev .
#
# The builder is pinned by digest (golang:1.26.9, resolved 2026-10-09 for
# the 2026-10-08 Go security release): it controls the output binary,
# so pin it like a dependency.
FROM golang:1.26.9@sha256:f1f0bcc2c524a3ced375fcb4d1ecb7aa371aa7070e112599aaca45cc02d0101b AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ddnscheck .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /ddnscheck /ddnscheck
USER 65534:65534
ENTRYPOINT ["/ddnscheck"]
