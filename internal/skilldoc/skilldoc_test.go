package skilldoc

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func tree() *cobra.Command {
	root := &cobra.Command{Use: "connect"}
	sms := &cobra.Command{Use: "sms"}
	send := &cobra.Command{Use: "send", Short: "Send one SMS", Annotations: map[string]string{"scope": "connect:sms.send"}, Run: func(*cobra.Command, []string) {}}
	send.Flags().String("to", "", "recipient")
	sms.AddCommand(send)
	sup := &cobra.Command{Use: "suppressions"}
	sup.AddCommand(&cobra.Command{Use: "remove NUMBER", Short: "Remove an opt-out", Annotations: map[string]string{"scope": "api-key-only"}, Run: func(*cobra.Command, []string) {}})
	root.AddCommand(sms, sup)
	return root
}

func TestSkillIsShortAndRoutesScopes(t *testing.T) {
	s := Skill(tree())
	if n := strings.Count(s, "\n"); n >= 300 {
		t.Fatal("too long", n)
	}
	if !strings.Contains(s, "| Remove an opt-out | `connect suppressions remove` | api-key-only |") {
		t.Fatal(s)
	}
}

func TestCommandsListsLeavesWithFlags(t *testing.T) {
	c := Commands(tree())
	if !strings.Contains(c, "### `connect sms send [flags]`") || !strings.Contains(c, "- `--to` recipient") || !strings.Contains(c, "## connect suppressions") {
		t.Fatal(c)
	}
}
