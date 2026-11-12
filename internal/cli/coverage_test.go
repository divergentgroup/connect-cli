package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The coverage gate: every operation in the public document is either covered by a command
// annotation or listed in openapi/exclusions.txt.
func TestEveryPublicOperationHasACommand(t *testing.T) {
	doc, err := os.ReadFile("../../openapi/public-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(doc, &spec); err != nil {
		t.Fatal(err)
	}
	excluded := map[string]bool{}
	raw, _ := os.ReadFile("../../openapi/exclusions.txt")
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			excluded[strings.Fields(l)[0]] = true
		}
	}
	covered := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, op := range strings.Split(c.Annotations["operation"], ",") {
			if op != "" {
				covered[op] = true
			}
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(NewRoot(&App{}))
	for path, ops := range spec.Paths {
		for method, op := range ops {
			if op.OperationID == "" {
				t.Errorf("%s %s has no operationId", method, path)
			}
			if !covered[op.OperationID] && !excluded[op.OperationID] {
				t.Errorf("operation %s (%s %s) has no command and is not excluded", op.OperationID, method, path)
			}
		}
	}
	for op := range excluded {
		if covered[op] {
			t.Errorf("operation %s is excluded but also covered; remove it from exclusions", op)
		}
	}
}
