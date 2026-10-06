// Package screen is how the CLI draws a terminal. Plain output stays
// aligned for pipes and docs. Color is added only when stdout is a terminal,
// and green and red are used only for an allow and a deny.
package screen

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	esc      = "\x1b["
	reset    = esc + "0m"
	dimCode  = esc + "2m"
	boldOn   = esc + "1m"
	allowOn  = esc + "32m"
	denyOn   = esc + "31m"
	accentOn = esc + "94m"
)

// Screen writes one command's output.
type Screen struct {
	w      io.Writer
	color  bool
	indent string
}

// New reports color only for a real terminal, and never when NO_COLOR is set.
func New(w io.Writer) Screen {
	return Screen{w: w, color: colorWanted(w)}
}

// Indented returns a Screen whose tables start n spaces in.
func (s Screen) Indented(n int) Screen {
	s.indent = strings.Repeat(" ", n)
	return s
}

func colorWanted(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// Label prints a 9-wide label and the rest of the line, matching %-9s.
func (s Screen) Label(label, text string) {
	fmt.Fprintf(s.w, "%s %s\n", s.dim(pad(label, 9)), text)
}

// Check prints a doctor row, matching %-4s  %-18s %s. FAIL is bold.
func (s Screen) Check(status, name, detail string) {
	st := pad(status, 4)
	if status == "FAIL" {
		st = s.bold(st)
	}
	fmt.Fprintf(s.w, "%s  %s %s\n", st, s.dim(pad(name, 18)), detail)
}

// Cell is one table cell. Tone is "", "allow", or "deny".
type Cell struct {
	Text string
	Tone string
}

// Table prints rows the way text/tabwriter does with padding 2.
// The first row is the header and is dim on a terminal.
func (s Screen) Table(rows [][]Cell) {
	if len(rows) == 0 {
		return
	}
	cols := 0
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	width := make([]int, cols)
	for _, row := range rows {
		for i, c := range row {
			if n := len(c.Text); n > width[i] {
				width[i] = n
			}
		}
	}
	for i := range width {
		if i < cols-1 {
			width[i] += 2
		}
	}
	for r, row := range rows {
		var b strings.Builder
		for i := 0; i < cols; i++ {
			text := ""
			tone := ""
			if i < len(row) {
				text, tone = row[i].Text, row[i].Tone
			}
			if i == cols-1 {
				b.WriteString(s.paint(text, tone))
				continue
			}
			gap := width[i] - len(text)
			if gap < 0 {
				gap = 0
			}
			b.WriteString(s.paint(text, tone))
			b.WriteString(strings.Repeat(" ", gap))
		}
		line := b.String()
		if r == 0 {
			line = s.dim(line)
		}
		fmt.Fprintln(s.w, s.indent+line)
	}
}

// Allow paints text only when it is an allow.
func (s Screen) Allow(text string) string { return s.paint(text, "allow") }

// Deny paints text only when it is a deny.
func (s Screen) Deny(text string) string { return s.paint(text, "deny") }

// Bold marks a command or a failure without using the deny color.
func (s Screen) Bold(text string) string { return s.bold(text) }

// Dim marks a label.
func (s Screen) Dim(text string) string { return s.dim(text) }

func (s Screen) paint(text, tone string) string {
	switch tone {
	case "allow":
		return s.wrap(text, allowOn)
	case "deny":
		return s.wrap(text, denyOn)
	case "accent":
		return s.wrap(text, accentOn)
	case "dim":
		return s.dim(text)
	default:
		return text
	}
}

func (s Screen) dim(text string) string  { return s.wrap(text, dimCode) }
func (s Screen) bold(text string) string { return s.wrap(text, boldOn) }

func (s Screen) wrap(text, code string) string {
	if !s.color || text == "" {
		return text
	}
	return code + text + reset
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
