# FetchDiff

FetchDiff monitors remote JavaScript and web pages for changes. It keeps exact snapshots, checks URLs on a schedule, prints readable full diffs, and can notify Slack, Discord, Telegram, email, and other providers.

![](image.png)

## Installation

```sh
go install github.com/BehiSecc/fetchdiff/cmd/fetchdiff@latest
sudo ln -sf "$(go env GOPATH)/bin/fetchdiff" /usr/local/bin/fetchdiff

fetchdiff init
```

This creates the private database, snapshots directory, and provider configuration under `~/.fetchdiff`. Then add a URL; the first fetch becomes its baseline:

```sh
fetchdiff add https://cdn.example.com/app.js \
  --name production-js \
  --every 24h
```

Intervals support minutes, hours, days, weeks, and combinations: `30m`, `24h`, `7d`, `2w`, or `1w2d`.

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

Slack, Discord, Telegram, SMTP, and opted-in custom webhooks receive the self-contained HTML diff file. For custom webhooks, set `custom_multipart: true`; FetchDiff sends `data` and `file` fields. Slack also needs `slack_token` with `files:write` and a channel ID in `slack_channel` (keep the webhook for normal alerts). Providers without file support receive the concise summary. Failed deliveries stay queued and retry without fetching the target again.

## Run continuously

Install the systemd service for your current user, then enable it:

```sh
sudo fetchdiff service install --user "$(id -un)"
sudo systemctl enable --now fetchdiff
```

Restart the service after changing `providers.yaml`:

```sh
sudo systemctl restart fetchdiff
```

## Common commands

```sh
fetchdiff list
fetchdiff show production-js
fetchdiff check
fetchdiff check --force
fetchdiff check production-js --force
fetchdiff remove production-js
fetchdiff history production-js
fetchdiff changes production-js
fetchdiff diff production-js
fetchdiff diff production-js --change CHANGE_ID
fetchdiff diff production-js --change CHANGE_ID --output change.html
fetchdiff status
fetchdiff doctor
```

`fetchdiff list` shows a compact colored table; set `NO_COLOR=1` to disable color. Use `fetchdiff show NAME` for the full URL and metadata. `fetchdiff check` checks due targets once and exits, while `fetchdiff watch` runs continuously.

## Useful flags

- `--name production-js` sets a target's unique name.
- `--every DURATION` sets the check interval.
- `--header "Name: value"` stores a repeatable request header with the target.
- `check --force` checks every target now; `check NAME --force` checks one target now.
- `remove NAME` deletes a target, its history, and its queued notifications.
- `history NAME --limit 50` controls displayed history; zero shows everything.
- `changes NAME` lists content changes and their IDs; `--limit 0` shows all.
- `diff NAME --change ID` selects one change; `--output FILE` writes it (`.html` creates the visual report).
- `--timeout 30s`, `--max-retries 3`, and `--max-redirects 10` control HTTP behavior.
- `--user-agent "MyMonitor/1.0"` changes the User-Agent for the current process.
- `--data-dir PATH` or `FETCHDIFF_DATA_DIR` changes the state directory.
