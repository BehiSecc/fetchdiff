# FetchDiff

FetchDiff monitors remote JavaScript and web pages for changes. It keeps exact snapshots, checks URLs on a schedule, prints readable full diffs, and can notify Slack, Discord, Telegram, email, and other providers.

## Installation

```sh
go install github.com/BehiSecc/fetchdiff/cmd/fetchdiff@latest
sudo ln -s "$(go env GOPATH)/bin/fetchdiff" /usr/local/bin/fetchdiff
```

Add a URL. The first fetch becomes its baseline:

```sh
fetchdiff add https://cdn.example.com/app.js \
  --name production-js \
  --every 24h
```

FetchDiff stores its private database, snapshots, and provider configuration under `~/.fetchdiff`.

## Notifications

The first FetchDiff command creates a commented template at:

```text
~/.fetchdiff/providers.yaml
```

Edit that file and uncomment only the providers you use. It follows the ProjectDiscovery Notify format and is protected with `0600` permissions.

If Notify is already configured, copy its configuration directly:

```sh
cp ~/.config/notify/provider-config.yaml ~/.fetchdiff/providers.yaml
chmod 600 ~/.fetchdiff/providers.yaml
fetchdiff notify-test
```

If you have several custom webhooks, keep them as entries under one `custom:` list; duplicate YAML keys are invalid.

Test a specific destination when needed:

```sh
fetchdiff notify-test --provider discord --id crawl
```

Slack, Discord, Telegram, Pushover, SMTP, Google Chat, Teams, Gotify, and custom webhooks are supported. FetchDiff sends change, third-failure, recovery, status, and redirect alerts. Failed deliveries stay queued and retry without fetching the target again.

## Run continuously

Create `/etc/systemd/system/fetchdiff.service` and replace `YOUR_USER` with your Linux username:

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

Restart the service after changing `providers.yaml`:

```sh
sudo systemctl restart fetchdiff
```

## Common commands

```sh
fetchdiff list
fetchdiff check
fetchdiff check production-js --force
fetchdiff history production-js
fetchdiff diff production-js
fetchdiff status
fetchdiff doctor
```

`fetchdiff check` checks due targets once and exits, which is useful for cron or CI. `fetchdiff watch` runs the scheduler continuously.

## Useful flags

- `--name production-js` sets a target's unique name.
- `--every 24h` sets its interval; durations include `30m`, `6h`, and `24h`.
- `--header "Name: value"` stores a repeatable request header with the target.
- `check NAME --force` checks immediately instead of waiting until the target is due.
- `history NAME --limit 50` controls displayed history; zero shows everything.
- `--timeout 30s`, `--max-retries 3`, and `--max-redirects 10` control HTTP behavior.
- `--user-agent "MyMonitor/1.0"` changes the User-Agent for the current process.
- `--data-dir PATH` or `FETCHDIFF_DATA_DIR` changes the state directory.
