package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableTTYAlignsAndPipedIsTSV(t *testing.T) {
	var b bytes.Buffer
	Printer{W: &b, TTY: true}.Table([]string{"ID", "NAME"}, [][]string{{"1", "a"}})
	if !strings.HasPrefix(b.String(), "ID  NAME\n1   a\n") {
		t.Fatalf("%q", b.String())
	}
	b.Reset()
	Printer{W: &b}.Table([]string{"ID", "NAME"}, [][]string{{"1", "a"}})
	if b.String() != "1\ta\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestRawReindentsJSON(t *testing.T) {
	var b bytes.Buffer
	_ = Printer{W: &b}.Raw([]byte(`{"a":1}`))
	if b.String() != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("%q", b.String())
	}
}

func TestServerTextCannotDriveTheTerminal(t *testing.T) {
	evil := "a\x1b]0;pwned\x07\x1b[2Jb\u009bc\nd"
	var b bytes.Buffer
	p := Printer{W: &b, TTY: true}
	p.Table([]string{"X"}, [][]string{{evil}})
	p.KV([][2]string{{"K", evil}})
	p.Println(evil)
	if strings.ContainsAny(b.String(), "\x1b\x07\u009b") || !strings.Contains(b.String(), "abcd\n") || !strings.Contains(b.String(), "abc\nd\n") {
		t.Fatalf("%q", b.String())
	}
}
