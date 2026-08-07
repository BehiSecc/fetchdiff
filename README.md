# FetchDiff

FetchDiff monitors remote JavaScript and web pages for content changes. It keeps exact snapshots, checks URLs on a schedule, and prints readable full diffs—even for minified JavaScript and HTML.

## Quick start

```sh
go build -o fetchdiff ./cmd/fetchdiff

./fetchdiff add https://cdn.example.com/app.js \
  --name production-js \
  --every 24h

./fetchdiff watch
```

For cron, systemd, or CI, check due targets once and exit:

```sh
fetchdiff check
```

Inspect a target manually:

```sh
fetchdiff check production-js --force
fetchdiff list
fetchdiff history production-js
fetchdiff diff production-js
fetchdiff status
fetchdiff doctor
```

FetchDiff stores private state under `~/.fetchdiff`: metadata in `state.db` and gzip-compressed snapshots under `snapshots/sha256`. Set `FETCHDIFF_DATA_DIR` or use `--data-dir` to override the location.

Notification delivery is intentionally not included yet. Change and failure events are printed clearly to stdout for future integration.

Requests default to a 30-second timeout, ten redirects, and three retries for transient failures. Use `--timeout`, `--max-redirects`, `--max-retries`, `--user-agent`, and repeatable `--header` flags when a target needs different HTTP behavior.
