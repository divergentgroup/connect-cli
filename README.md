# connect

`connect` is the command-line client for the Connect messaging API v2 (`https://api.connect.ms/v2`, the API behind PureSMS): SMS and email sending,
contacts, lists, opt-outs, reports, and a local MCP proxy so coding agents can use the hosted Connect MCP
server with your stored credential.

## Install

```sh
brew install divergentgroup/tap/connect
curl -fsSL https://connect.ms/install.sh | sh    # verifies checksums, installs to /usr/local/bin or ~/.local/bin
```

Or download an archive from the releases page. Build from source with `go build ./cmd/connect`.

## Authenticate

```sh
connect auth login                 # browser sign-in (PKCE); add --no-browser on a headless machine
connect auth login --api-key       # reads a workspace API key from stdin, never from an argument
connect auth status
```

### Sign up

An agent can create a workspace for a person who has no account. Ask the person for each code, and show them
the [online services agreement](https://divergent.group/legal/online-services-agreement); pass `--agree-terms` only once they agree.

```sh
connect auth signup --name "Ada Lovelace" --email ada@example.com --phone +447700900000 --workspace "Analytical Ltd" --agree-terms
connect auth signup verify CODE    # for each code the person receives
connect auth signup resend         # if a code did not arrive
```

`CONNECT_API_KEY` in the environment overrides any stored profile, which suits CI. Keys are never echoed,
never accepted as arguments and never written to logs. With `--no-keyring` (or when no OS keyring exists)
credentials are stored in `~/.config/connect/credentials.json` with mode 0600; on Unix that file holds the
refresh token in clear text, so protect the account. On macOS the keychain item is readable by other programs
running as the same user.

Signed-in grants are limited to the scopes you tick; live sending and set-up (requesting sender names, adding
email domains and test recipients) are opt-in on the sign-in page. Commands marked api-key-only (anything that
deletes or cancels a sender request, removing opt-outs, SMS and email template writes, MJML includes, webhooks,
API keys) are refused under a browser login with exit 3.
API keys with Strict API Mode need every request signed, which the CLI cannot do: use a key without it, or
`connect auth login`.

## Use

```sh
connect workspace show
connect sms send --to +447700900001 --from Acme --content "Hello"        # test workspaces hit the simulator
connect sms get <id>
connect sms events <id>                                                  # the message's events from the last seven days
connect sms search --number +447700900001 --since 2026-09-01T00:00:00Z --limit 100
connect sms send-bulk --file messages.csv                                # chunked, idempotent, resumable
connect sms send-list --list <id> --from Acme --content "Sale on" --dry-run
connect email domains add mail.example.com                                # prints the DNS records to publish
connect email domains verify <id>
connect email send --to ann@example.com --from hello@mail.example.com --subject "Hello" --html-file body.html --text "Hello"
connect email send --to ann@example.com --from hello@mail.example.com --subject "Welcome" --template welcome --data '{"name":"Ann"}'
connect email list --status failed --since 2026-11-01T00:00:00Z
connect email test-recipients add me@example.com                         # confirm via the emailed link, then send to it from the test sender
connect reports email-deliverability --from 2026-11-01 --to 2026-11-07
connect contacts upsert --file contacts.csv --map Mobile=phone
connect reports analytics --from 2026-09-01 --to 2026-09-05
connect billing balance
```

Output is a table on a terminal, tab-separated when piped, and the raw API JSON with `--json`.
Lists are cursor paged: `--limit` (1 to 200, default 50) sets the page size, and when there is another page
the CLI prints the `--cursor` value to pass on stderr. Enum values are snake_case, as in the API.
Simulator handsets: `+447700900001` delivers, `+447700900002` fails, `+447700900003` replies.

### Forward events to a local server

```sh
connect listen --forward-to http://localhost:3000/webhooks
connect listen --forward-to http://localhost:3000/webhooks --events sms.delivered,sms.received --secret "$SECRET"
```

`connect listen` polls the event feed and POSTs every event that happens after it starts to your URL, with
the same body and signature headers (`Content-Digest`, `Webhook-Id`, `Signature-Input`, `Signature`; RFC 9421,
hmac-sha256, keyid 1) as a real webhook delivery, so your handler's verification runs unchanged. It prints the
signing secret at start; pass `--secret` to keep one across runs. Events arrive a second or two after they happen
(`--interval`, default 2s). The URL must be https, or http on localhost.

### Exit codes

| Code | Meaning |
| --- | --- |
| 2 | usage error |
| 3 | not signed in, token rejected, missing scope (required scope printed), Strict API Mode key, or sending blocked |
| 4 | validation error (field errors printed one per line), not found or conflict |
| 5 | upstream error, rate limited or API key limit reached (Retry-After printed), or unavailable |
| 6 | payment required (billing URL printed) |

### MCP for coding agents

```json
{ "mcpServers": { "connect": { "command": "connect", "args": ["mcp"] } } }
```

`connect mcp` mirrors the hosted Connect MCP tools over stdio and forwards each call with your credential.

## Development

```sh
scripts/openapi-refresh.sh   # downloads the v2 document into openapi/public-v2.json
go generate ./...     # regenerates internal/api/client.gen.go from openapi/public-v2.json
go test ./...         # includes the coverage gate: every public operation must map to a command
scripts/smoke.sh      # manual end-to-end run against a test workspace (needs CONNECT_API_KEY)
```

`openapi/public-v2.json` is Connect's published v2 document, with single-entry `oneOf` wrappers unwrapped by
the refresh script so the generated models are plain structs. After a refresh, fix any compile errors, add
operations that should not be commands to `openapi/exclusions.txt`, and make the coverage gate green again.
Every request sends `Connect-Version: 2026-10-01` (`internal/config.APIVersion`).

Pushing a `v*` tag runs the release workflow: GoReleaser builds every platform, signs and notarises the macOS
binaries, publishes the release and updates the `divergentgroup/homebrew-tap` cask.

## Licence

MIT.
