// Package output renders tables on a TTY, TSV when piped, and raw JSON under --json.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
	"unicode"

	"golang.org/x/term"
)

type Printer struct {
	W    io.Writer
	JSON bool
	TTY  bool
}

func New(w io.Writer, asJSON bool) Printer {
	p := Printer{W: w, JSON: asJSON}
	if f, ok := w.(*os.File); ok {
		p.TTY = term.IsTerminal(int(f.Fd()))
	}
	return p
}

// Raw writes an API body verbatim, re-indented when it is JSON.
func (p Printer) Raw(body []byte) error {
	if len(body) == 0 {
		_, err := fmt.Fprintln(p.W, "{}")
		return err
	}
	var v any
	if json.Unmarshal(body, &v) == nil {
		enc := json.NewEncoder(p.W)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := fmt.Fprintln(p.W, strings.TrimSpace(string(body)))
	return err
}

// escapes matches terminal escape sequences: CSI, OSC (ended by BEL or ST) and two-byte escapes.
var escapes = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|.?)`)

func clean(s, keep string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !strings.ContainsRune(keep, r) {
			return -1
		}
		return r
	}, escapes.ReplaceAllString(s, ""))
}

// Clean strips escape sequences and control characters other than tab from server text bound for a terminal.
func Clean(s string) string { return clean(s, "\t") }

// CleanText is Clean for multi-line text: newlines are kept.
func CleanText(s string) string { return clean(s, "\t\n") }

func cleanRow(r []string) string {
	c := make([]string, len(r))
	for i, v := range r {
		c[i] = strings.ReplaceAll(Clean(v), "\t", " ")
	}
	return strings.Join(c, "\t")
}

func (p Printer) Table(headers []string, rows [][]string) {
	if !p.TTY {
		for _, r := range rows {
			fmt.Fprintln(p.W, cleanRow(r))
		}
		return
	}
	tw := tabwriter.NewWriter(p.W, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, cleanRow(r))
	}
	_ = tw.Flush()
}

// KV prints label/value pairs, one per line, leaving out empty values.
func (p Printer) KV(pairs [][2]string) {
	tw := tabwriter.NewWriter(p.W, 0, 0, 2, ' ', 0)
	for _, kv := range pairs {
		if kv[1] == "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\n", kv[0], Clean(kv[1]))
	}
	_ = tw.Flush()
}

func (p Printer) Println(a ...any) { fmt.Fprint(p.W, CleanText(fmt.Sprintln(a...))) }
