# FetchDiff

FetchDiff monitors remote JavaScript and web pages for content changes. It keeps exact snapshots, checks URLs on a schedule, and prints readable full diffs—even for minified JavaScript and HTML.

## Installation

```sh
go install github.com/BehiSecc/fetchdiff/cmd/fetchdiff@latest
sudo ln -s "$(go env GOPATH)/bin/fetchdiff" /usr/local/bin/fetchdiff
```

To run FetchDiff continuously, create `/etc/systemd/system/fetchdiff.service` and replace `YOUR_USER` with your Linux username:

```ini
[Unit]
Description=FetchDiff URL monitor
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=YOUR_USER
Environment=FETCHDIFF_DATA_DIR=/home/YOUR_USER/.fetchdiff
ExecStart=/usr/local/bin/fetchdiff watch
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable it:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now fetchdiff
```

## Examples

Add a URL and create its initial snapshot:

```sh
fetchdiff add https://cdn.example.com/app.js \
  --name production-js \
  --every 24h
```

Inspect or check targets:

```sh
fetchdiff list
fetchdiff check production-js --force
fetchdiff history production-js
fetchdiff diff production-js
fetchdiff status
fetchdiff doctor
```

Run the scheduler directly instead of using systemd:

```sh
fetchdiff watch
```

For cron, systemd, or CI, check due targets once and exit:

```sh
fetchdiff check
```

FetchDiff stores private state under `~/.fetchdiff`: metadata in `state.db` and gzip-compressed snapshots under `snapshots/sha256`. Set `FETCHDIFF_DATA_DIR` or use `--data-dir` to override the location.

Notification delivery is intentionally not included yet. Change and failure events are printed clearly to stdout for future integration.

Requests default to a 30-second timeout, ten redirects, and three retries for transient failures. Use `--timeout`, `--max-redirects`, `--max-retries`, `--user-agent`, and repeatable `--header` flags when a target needs different HTTP behavior.
