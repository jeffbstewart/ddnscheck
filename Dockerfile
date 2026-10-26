# ddnscheck -- stdlib-only static binary on scratch: non-root, no caps,
# nothing in the image but the binary and CA roots.  CA roots ARE
# required: the WAN-echo lookups are HTTPS.
#
# CI (.github/workflows/ci.yml) builds and pushes this to
# ghcr.io/jeffbstewart/ddnscheck on push to main and on version tags.
# To build locally from the repo root:  docker build -t ddnscheck:dev .
#
# The builder is pinned by digest (golang:1.26.10, resolved 2026-10-26 for
# the 2026-10-21 Go security release): it controls the output binary,
# so pin it like a dependency.
FROM golang:1.26.10@sha256:672c944cfd07599b5f1bb179869270cdc94c6254e41db4d431259693cb286b82 AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ddnscheck .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /ddnscheck /ddnscheck
USER 65534:65534
ENTRYPOINT ["/ddnscheck"]
