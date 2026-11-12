// Package skilldoc renders SKILL.md and reference/commands.md from the command tree,
// so the agent-facing documentation can never drift from the CLI.
package skilldoc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Write renders both documents under dir.
func Write(root *cobra.Command, dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "reference"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(Skill(root)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "reference", "commands.md"), []byte(Commands(root)), 0o644)
}

func leaves(c *cobra.Command, visit func(*cobra.Command)) {
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		if sub.HasSubCommands() {
			leaves(sub, visit)
			continue
		}
		visit(sub)
	}
}

// Skill renders the agent skill document.
func Skill(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString(`---
name: connect
description: Send SMS and email, manage contacts and lists, and read reports through the Connect API v2 using the connect CLI. Use when the user asks to text or email someone, run an SMS campaign, set up an email sending domain, manage opt-outs or check delivery.
---

# connect CLI

## Check it is installed

Run ` + "`connect version`" + `. If it is missing, install with ` + "`brew install divergentgroup/tap/connect`" + ` or ` + "`curl -fsSL https://connect.ms/install.sh | sh`" + `.

## Authentication

- Interactive: ` + "`connect auth login`" + ` opens the browser (PKCE). Headless: add ` + "`--no-browser`" + ` and paste the redirect URL.
- API key: ` + "`connect auth login --api-key`" + ` reads the key from stdin, or set ` + "`CONNECT_API_KEY`" + ` in the environment.
- Never put an API key in a command line argument, a log, or a file you commit. Keys are never echoed by the CLI.
- ` + "`connect auth status`" + ` shows which credential and workspace are active; ` + "`--profile NAME`" + ` switches.

## Safety rules

- Always ask the user for the recipient, sender and content before sending. Send once; do not retry a send that returned an id.
- The default ` + "`--kind`" + ` is transactional. Use ` + "`--kind marketing`" + ` only for opted-in campaigns.
- Confirm with the user before ` + "`sms send-bulk`" + ` or ` + "`sms send-list`" + `; run ` + "`sms send-list --dry-run`" + ` first to show the count and cost.
- Test workspaces only reach simulator handsets: +447700900001 delivers, +447700900002 fails, +447700900003 replies. Read replies with ` + "`sms simulator phones`" + `.
- Email goes from an address on a verified sending domain: ` + "`email domains add`" + ` prints the DNS records to publish, then ` + "`email domains verify`" + `. Until then, the shared test sender only reaches confirmed ` + "`email test-recipients`" + `.
- ` + "`email send`" + ` takes ` + "`--html`" + `/` + "`--text`" + ` (or ` + "`--html-file`" + `/` + "`--text-file`" + `), or ` + "`--template NAME`" + ` with ` + "`--data`" + `. Send once; pass ` + "`--idempotency-key`" + ` if you may need to retry.
- Prefer ` + "`--json`" + ` when you need to parse output; the human table is for the user.
- Lists are cursor paged: 50 rows by default, ` + "`--limit`" + ` up to 200. When there is more, stderr says which ` + "`--cursor`" + ` to pass (under ` + "`--json`" + ` read ` + "`nextCursor`" + `).
- Ids are strings and enum values are snake_case, e.g. ` + "`--status delivered`" + `, ` + "`--rule tag:equals:vip`" + `.

## Errors and exit codes

| Exit | Meaning | What to do |
| --- | --- | --- |
| 2 | usage | fix the flags; run the command with ` + "`--help`" + ` |
| 3 | auth, missing scope, Strict API Mode key or sending blocked | tell the user to run ` + "`connect auth login`" + ` and grant the listed scope, or use an API key for api-key-only commands; a Strict API Mode key cannot be used by the CLI |
| 4 | validation, not found or conflict | field errors are printed one per line as ` + "`field: message`" + `; fix the input |
| 5 | upstream, rate limited, API key limit or unavailable | wait for the printed Retry-After and retry once |
| 6 | payment required | tell the user to top up at the printed billing URL; do not retry |

With ` + "`--json`" + ` the error object is printed to stdout as ` + "`{\"error\",\"message\",\"required\",\"fieldErrors\",\"requestId\"}`" + `.

## Command routing

Scope ` + "`api-key-only`" + ` means the command is refused under a browser login; ` + "`any`" + ` works with every credential.

| Intent | Command | Scope |
| --- | --- | --- |
`)
	leaves(root, func(c *cobra.Command) {
		if c.Name() == "version" || c.Name() == "docs" {
			return
		}
		b.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", c.Short, c.CommandPath(), scopeOf(c)))
	})
	b.WriteString("\nFull flag reference: reference/commands.md\n")
	return b.String()
}

func scopeOf(c *cobra.Command) string {
	if s := c.Annotations["scope"]; s != "" {
		return s
	}
	return "any"
}

// Commands renders every leaf command with its flags, grouped by parent.
func Commands(root *cobra.Command) string {
	var b strings.Builder
	b.WriteString("# connect command reference\n\nGlobal flags: `--json`, `--profile NAME`, `--workspace ID` (API-key credentials only), `--api-url URL`, `--no-keyring`.\n")
	var group string
	leaves(root, func(c *cobra.Command) {
		if p := c.Parent().CommandPath(); p != group {
			group = p
			b.WriteString("\n## " + group + "\n")
		}
		b.WriteString(fmt.Sprintf("\n### `%s`\n\n%s\n", c.UseLine(), c.Short))
		if c.Long != "" {
			b.WriteString("\n" + c.Long + "\n")
		}
		if s := c.Annotations["scope"]; s != "" {
			b.WriteString("\nScope: `" + s + "`\n")
		}
		var flags []string
		c.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			def := ""
			if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
				def = " (default " + f.DefValue + ")"
			}
			flags = append(flags, fmt.Sprintf("- `--%s` %s%s", f.Name, f.Usage, def))
		})
		if len(flags) > 0 {
			b.WriteString("\n" + strings.Join(flags, "\n") + "\n")
		}
	})
	return b.String()
}
