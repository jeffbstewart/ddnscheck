# ddnscheck

An independent efficacy monitor for dynamic DNS. It answers one
question -- *does the published DNS record actually point at this
network's WAN egress IP?* -- via paths deliberately disjoint from
whatever updates the record, so the updater and the checker cannot be
wrong together.

- The published record is resolved against a **public** DNS resolver
  (default `1.1.1.1:53`), dialed directly -- never through a
  split-horizon internal resolver that might answer with an internal
  address for the same name.
- The WAN egress IP comes from **HTTPS what-is-my-ip** services
  (default `checkip.amazonaws.com`, `api.ipify.org`), independent of any
  DNS-based IP discovery the updater uses.

It exports Prometheus metrics and is meant to run beside -- but on
different machinery from -- your dynamic-DNS updater.

## Usage

    ddnscheck [flags]

| Flag | Default | Meaning |
| --- | --- | --- |
| `-record` | `home.stewart.net` | DNS record to verify |
| `-resolver` | `1.1.1.1:53` | public DNS resolver (host:port); must NOT be a split-horizon resolver |
| `-wan-urls` | `https://checkip.amazonaws.com,https://api.ipify.org` | comma-separated HTTPS services that echo the caller's IP, tried in order |
| `-port` | `9878` | metrics listener port |
| `-interval` | `2m` | time between checks |
| `-timeout` | `15s` | per-check deadline |

Metrics are served at `/metrics` on the listener port. Key series:

- `homenet_ddns_record_matches_wan` -- `1` when the record's public
  answer equals the WAN egress IP (the headline verdict).
- `homenet_ddns_published_ip` / `homenet_ddns_wan_ip` -- info gauges
  carrying the two compared addresses as labels.
- `homenet_ddns_last_success_timestamp_seconds` -- staleness guard.
- `homenet_ddns_check_errors_total{stage="resolve"|"wan"}` -- failures
  by stage.

## Build

Pure Go standard library -- no third-party dependencies.

    go build ./...
    go test ./...

Container image (stdlib static binary on `scratch`, non-root):

    docker build -t ddnscheck:dev .

CI publishes `ghcr.io/jeffbstewart/ddnscheck` on pushes to `main` and on
version tags.

## Development

    sh scripts/install-hooks.sh   # wire the presubmit as the pre-commit hook
    sh scripts/presubmit.sh       # 7-bit ASCII, gofmt, go vet, tests

All committed text is 7-bit ASCII with LF line endings.

## License

Apache License 2.0 -- see [LICENSE](LICENSE).
