package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestApp() (*App, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &App{In: strings.NewReader(""), Out: out, Err: errb}, out, errb
}

func TestUnknownFlagExitsTwo(t *testing.T) {
	app, _, errb := newTestApp()
	if code := run(NewRoot(app), app, []string{"version", "--bogus"}); code != 2 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
}

func TestVersionJSON(t *testing.T) {
	app, out, _ := newTestApp()
	if code := run(NewRoot(app), app, []string{"version", "--json"}); code != 0 || !strings.Contains(out.String(), `"version"`) {
		t.Fatal(out.String())
	}
}

func TestSkillDocFromRealTreeIsShortAndComplete(t *testing.T) {
	app, out, errb := newTestApp()
	dir := t.TempDir()
	if code := run(NewRoot(app), app, []string{"internal", "skill-doc", "--out", dir}); code != 0 {
		t.Fatal(code, out.String(), errb.String())
	}
	skill, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if n := strings.Count(string(skill), "\n"); n >= 300 || !strings.Contains(string(skill), "`connect suppressions remove` | api-key-only") {
		t.Fatal(n, string(skill))
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "reference", "commands.md"))
	if !strings.Contains(string(ref), "connect sms send") {
		t.Fatal(string(ref))
	}
}
