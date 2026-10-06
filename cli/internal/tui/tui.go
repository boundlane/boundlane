// Package tui draws the guided setup: a framed welcome, steps, checks with a
// spinner, and questions answered with the arrow keys. Without a terminal it
// falls back to numbered questions read line by line, so a script or a test
// can answer them.
//
// Colour follows the brand: an accent for structure, green and red only for
// an allow and a deny, and no colour at all when NO_COLOR is set.
package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// ErrCancelled means the person pressed Ctrl-C or closed the input.
var ErrCancelled = errors.New("cancelled")

// ErrBack means the person asked to go back to the previous question, with
// Esc or the left arrow. Without a terminal it is "b" or "<" for a choice and
// "<" for typed text. Only questions asked with back allowed return it.
var ErrBack = errors.New("back")

const (
	csi       = "\x1b["
	reset     = csi + "0m"
	boldCode  = csi + "1m"
	dimCode   = csi + "2m"
	accent    = csi + "94m"
	allowCode = csi + "32m"
	denyCode  = csi + "31m"
	hide      = csi + "?25l"
	show      = csi + "?25h"
	clearLine = "\r" + csi + "2K"
)

// LabelWidth is the column the detail of a check starts at.
const LabelWidth = 17

// UI is one interactive session.
type UI struct {
	in    io.Reader
	out   io.Writer
	r     *bufio.Reader
	fd    int
	tty   bool
	color bool
}

// New uses raw keys only when both in and out are terminals.
func New(in io.Reader, out io.Writer) *UI {
	u := &UI{in: in, out: out, r: bufio.NewReader(in), fd: -1}
	fin, okIn := in.(*os.File)
	fout, okOut := out.(*os.File)
	if okIn && okOut && term.IsTerminal(int(fin.Fd())) && term.IsTerminal(int(fout.Fd())) {
		u.tty = true
		u.fd = int(fin.Fd())
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	u.color = okOut && term.IsTerminal(int(fout.Fd())) && !noColor && os.Getenv("TERM") != "dumb"
	return u
}

// ForOutput draws without asking anything, in colour when w is a terminal.
func ForOutput(w io.Writer) *UI {
	return New(strings.NewReader(""), w)
}

// Interactive reports whether questions use the arrow keys.
func (u *UI) Interactive() bool { return u.tty }

// Restore shows the cursor again after an interrupt.
func (u *UI) Restore() {
	if u.tty {
		fmt.Fprint(u.out, show)
	}
}

func (u *UI) paint(code, s string) string {
	if !u.color || s == "" {
		return s
	}
	return code + s + reset
}

func (u *UI) Accent(s string) string { return u.paint(accent, s) }
func (u *UI) Bold(s string) string   { return u.paint(boldCode, s) }
func (u *UI) Dim(s string) string    { return u.paint(dimCode, s) }
func (u *UI) Allow(s string) string  { return u.paint(allowCode, s) }
func (u *UI) Deny(s string) string   { return u.paint(denyCode, s) }

// Size is the terminal's columns and rows, or 80 by 24 without one.
func (u *UI) Size() (int, int) {
	if u.tty {
		if fout, ok := u.out.(*os.File); ok {
			if w, h, err := term.GetSize(int(fout.Fd())); err == nil && w > 20 && h > 2 {
				return w, h
			}
		}
	}
	return 80, 24
}

func (u *UI) width() int {
	if u.tty {
		if fout, ok := u.out.(*os.File); ok {
			if w, _, err := term.GetSize(int(fout.Fd())); err == nil && w > 20 {
				return w
			}
		}
	}
	return 80
}

// Println writes one line.
func (u *UI) Println(s string) { fmt.Fprintln(u.out, s) }

// Blank writes an empty line.
func (u *UI) Blank() { fmt.Fprintln(u.out) }

// Box draws a framed panel. The title sits in the top border.
func (u *UI) Box(title string, lines []string) {
	w := min(u.width()-4, 64)
	inner := w - 6
	var wrapped []string
	for _, l := range lines {
		if l == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, wrap(l, inner)...)
	}
	top := u.Accent("┌" + strings.Repeat("─", w-2) + "┐")
	if title != "" {
		t := " " + title + " "
		top = u.Accent("┌─") + u.Bold(t) + u.Accent(strings.Repeat("─", max(0, w-3-visible(t)))+"┐")
	}
	fmt.Fprintln(u.out, "  "+top)
	side := u.Accent("│")
	fmt.Fprintln(u.out, "  "+side+strings.Repeat(" ", w-2)+side)
	for _, l := range wrapped {
		pad := inner - visible(l)
		fmt.Fprintln(u.out, "  "+side+"  "+l+strings.Repeat(" ", max(0, pad))+"  "+side)
	}
	fmt.Fprintln(u.out, "  "+side+strings.Repeat(" ", w-2)+side)
	fmt.Fprintln(u.out, "  "+u.Accent("└"+strings.Repeat("─", w-2)+"┘"))
}

// Step starts a numbered section.
func (u *UI) Step(n, total int, title string) {
	fmt.Fprintln(u.out)
	fmt.Fprintf(u.out, "  %s %s  %s\n", u.Accent("■"), u.Bold(title), u.Dim(fmt.Sprintf("%d of %d", n, total)))
	fmt.Fprintln(u.out)
}

// Title opens a command's output, with a blank line on each side.
func (u *UI) Title(title, detail string) {
	fmt.Fprintln(u.out)
	if detail == "" {
		fmt.Fprintf(u.out, "  %s %s\n", u.Accent("■"), u.Bold(title))
	} else {
		fmt.Fprintf(u.out, "  %s %s  %s\n", u.Accent("■"), u.Bold(title), u.Dim(detail))
	}
	fmt.Fprintln(u.out)
}

// Denied is something refused, in the red pen.
func (u *UI) Denied(label, detail string) {
	fmt.Fprintf(u.out, "  %s %s %s\n", u.Deny("✗"), u.Deny(padRight(label, LabelWidth)), detail)
}

// Done is a check that passed.
func (u *UI) Done(label, detail string) {
	fmt.Fprintf(u.out, "  %s %s %s\n", u.Accent("✓"), padRight(label, LabelWidth), detail)
}

// Problem is a check that needs the person.
func (u *UI) Problem(label, detail string) {
	fmt.Fprintf(u.out, "  %s %s %s\n", u.Bold("!"), u.Bold(padRight(label, LabelWidth)), detail)
}

// Skip is a step the person chose to leave for later.
func (u *UI) Skip(label, detail string) {
	fmt.Fprintf(u.out, "  %s %s %s\n", u.Dim("–"), padRight(label, LabelWidth), u.Dim(detail))
}

// Note is an indented explanation under a check or question.
func (u *UI) Note(text string) {
	for _, l := range wrap(text, min(u.width()-6, 72)) {
		fmt.Fprintln(u.out, "    "+u.Dim(l))
	}
}

// Command shows a command the person can type later.
func (u *UI) Command(cmd, why string) {
	if why == "" {
		fmt.Fprintf(u.out, "    %s\n", u.Bold(cmd))
		return
	}
	fmt.Fprintf(u.out, "    %s  %s\n", u.Bold(padRight(cmd, 24)), u.Dim(why))
}

// Commands shows several commands with their reasons in one column.
func (u *UI) Commands(pairs ...[2]string) {
	width := 0
	for _, p := range pairs {
		width = max(width, utf8.RuneCountInString(p[0]))
	}
	for _, p := range pairs {
		fmt.Fprintf(u.out, "    %s  %s\n", u.Bold(padRight(p[0], width)), u.Dim(p[1]))
	}
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spin runs fn while a spinner shows label, then prints Done or Problem.
// fn returns the detail to show and must not write to the terminal.
func (u *UI) Spin(label, working string, fn func() (string, error)) (string, error) {
	if !u.tty {
		detail, err := fn()
		u.result(label, detail, err)
		return detail, err
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Fprint(u.out, hide)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			fmt.Fprintf(u.out, "%s  %s %s %s", clearLine, u.Accent(frames[i%len(frames)]), padRight(label, LabelWidth), u.Dim(working))
			select {
			case <-stop:
				fmt.Fprint(u.out, clearLine+show)
				return
			case <-t.C:
			}
		}
	}()
	detail, err := fn()
	close(stop)
	wg.Wait()
	u.result(label, detail, err)
	return detail, err
}

func (u *UI) result(label, detail string, err error) {
	if err != nil {
		u.Problem(label, firstLine(err.Error()))
		return
	}
	u.Done(label, detail)
}

// Option is one answer to a Select.
type Option struct {
	Label    string
	Hint     string
	Disabled bool
}

// Select asks one question and returns the chosen index.
func (u *UI) Select(question string, opts []Option, def int) (int, error) {
	return u.Choose(question, opts, def, false)
}

// Choose is Select that can also return ErrBack when back is true.
func (u *UI) Choose(question string, opts []Option, def int, back bool) (int, error) {
	if def < 0 || def >= len(opts) || opts[def].Disabled {
		def = -1
		for i, o := range opts {
			if !o.Disabled {
				def = i
				break
			}
		}
	}
	if def < 0 {
		return -1, errors.New("no answer is available")
	}
	if !u.tty {
		return u.selectLines(question, opts, def, back)
	}
	return u.selectKeys(question, opts, def, back)
}

func (u *UI) selectLines(question string, opts []Option, def int, back bool) (int, error) {
	fmt.Fprintf(u.out, "  ? %s\n", question)
	for i, o := range opts {
		line := fmt.Sprintf("    %d. %s", i+1, o.Label)
		if o.Hint != "" {
			line += "  (" + o.Hint + ")"
		}
		fmt.Fprintln(u.out, line)
	}
	if back {
		fmt.Fprintln(u.out, "    b. Back")
	}
	for {
		fmt.Fprintf(u.out, "  Choose 1-%d [%d]: ", len(opts), def+1)
		line, err := u.readLine()
		if err != nil {
			return -1, err
		}
		if line == "" {
			u.answered(question, opts[def].Label)
			return def, nil
		}
		if back && (line == "b" || line == "B" || line == "<") {
			return -1, ErrBack
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(opts) && !opts[n-1].Disabled {
			u.answered(question, opts[n-1].Label)
			return n - 1, nil
		}
		fmt.Fprintln(u.out, "  That is not one of the choices.")
	}
}

func (u *UI) selectKeys(question string, opts []Option, cur int, back bool) (int, error) {
	state, err := term.MakeRaw(u.fd)
	if err != nil {
		return u.selectLines(question, opts, cur, back)
	}
	restore := func() { _ = term.Restore(u.fd, state); fmt.Fprint(u.out, show) }
	defer restore()

	labelW := 0
	for _, o := range opts {
		labelW = max(labelW, visible(o.Label))
	}
	maxW := u.width() - 1
	lines := 0
	draw := func() {
		var b strings.Builder
		if lines > 0 {
			fmt.Fprintf(&b, "\r"+csi+"%dA", lines)
		}
		row := func(s string) {
			b.WriteString(clearLine + s + "\r\n")
		}
		row(fmt.Sprintf("  %s %s", u.Accent("?"), u.Bold(truncate(question, maxW-4))))
		for i, o := range opts {
			text := padRight(o.Label, labelW)
			if o.Hint != "" {
				text += "  " + o.Hint
			}
			text = truncate(text, maxW-6)
			switch {
			case o.Disabled:
				row("      " + u.Dim(text))
			case i == cur:
				row("    " + u.Accent("❯ "+text))
			default:
				row("      " + text)
			}
		}
		hint := "↑↓ move · enter choose"
		if back {
			hint += " · esc back"
		}
		row("    " + u.Dim(hint))
		lines = len(opts) + 2
		fmt.Fprint(u.out, hide+b.String())
	}
	move := func(d int) {
		for i := 1; i <= len(opts); i++ {
			n := ((cur+d*i)%len(opts) + len(opts)) % len(opts)
			if !opts[n].Disabled {
				cur = n
				return
			}
		}
	}
	draw()
	for {
		k, err := u.readKey()
		if err != nil {
			u.erase(lines)
			return -1, err
		}
		switch k {
		case keyUp, 'k':
			move(-1)
		case keyDown, 'j':
			move(1)
		case keyEnter:
			u.erase(lines)
			restore()
			u.answered(question, opts[cur].Label)
			return cur, nil
		case keyCancel:
			u.erase(lines)
			return -1, ErrCancelled
		case keyBack, keyLeft:
			if back {
				u.erase(lines)
				return -1, ErrBack
			}
		default:
			if k >= '1' && k <= '9' {
				if n := int(k - '1'); n < len(opts) && !opts[n].Disabled {
					cur = n
				}
			}
		}
		draw()
	}
}

func (u *UI) erase(lines int) {
	if lines == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\r"+csi+"%dA", lines)
	for i := 0; i < lines; i++ {
		b.WriteString(clearLine + "\r\n")
	}
	fmt.Fprintf(&b, "\r"+csi+"%dA", lines)
	fmt.Fprint(u.out, b.String())
}

func (u *UI) answered(question, answer string) {
	fmt.Fprintf(u.out, "  %s %s %s\n", u.Accent("?"), question, u.Accent(answer))
}

// Confirm is a Select with two answers. Yes is the default.
func (u *UI) Confirm(question, yes, no string) (bool, error) {
	i, err := u.Select(question, []Option{{Label: yes}, {Label: no}}, 0)
	return i == 0, err
}

// Input asks for a line of text. An empty answer takes def.
func (u *UI) Input(question, def string) (string, error) {
	return u.Ask(question, def, false)
}

// Ask is Input that can also return ErrBack when back is true.
func (u *UI) Ask(question, def string, back bool) (string, error) {
	var line string
	var err error
	if u.tty {
		line, err = u.editLine(question, def, false, back)
	} else {
		prompt := fmt.Sprintf("  ? %s ", question)
		if def != "" {
			prompt += "(" + def + ") "
		}
		if back {
			prompt += "[< back] "
		}
		fmt.Fprint(u.out, prompt+"› ")
		line, err = u.readLine()
		if err == nil && back && line == "<" {
			return "", ErrBack
		}
	}
	if err != nil {
		return "", err
	}
	if line == "" {
		line = def
	}
	if u.tty {
		u.answered(question, line)
	}
	return line, nil
}

// Secret asks for a value without showing it.
func (u *UI) Secret(question string) (string, error) {
	return u.AskSecret(question, false)
}

// AskSecret is Secret that can also return ErrBack when back is true.
func (u *UI) AskSecret(question string, back bool) (string, error) {
	if !u.tty {
		hint := ""
		if back {
			hint = "[< back] "
		}
		fmt.Fprintf(u.out, "  ? %s %s› ", question, hint)
		s, err := u.readLine()
		if err == nil && back && s == "<" {
			return "", ErrBack
		}
		return s, err
	}
	s, err := u.editLine(question, "paste it here, it is not shown", true, back)
	if err != nil {
		return "", err
	}
	if s != "" {
		u.answered(question, fmt.Sprintf("received, %d characters", utf8.RuneCountInString(s)))
	}
	return s, nil
}

func (u *UI) readLine() (string, error) {
	line, err := u.r.ReadString('\n')
	if err != nil && line == "" {
		return "", ErrCancelled
	}
	return strings.TrimSpace(line), nil
}

const (
	keyUp = iota + 0x110000
	keyDown
	keyLeft
	keyRight
	keyEnter
	keyCancel
	keyBack
	keyErase
	keyKill
	keyNone
)

// readKey returns a rune, or one of the key constants above. A lone Esc is
// keyBack: terminals send escape sequences in one write, so an Esc with
// nothing buffered after it was typed on its own.
func (u *UI) readKey() (int, error) {
	r, _, err := u.r.ReadRune()
	if err != nil {
		return 0, ErrCancelled
	}
	switch r {
	case 3, 4:
		return keyCancel, nil
	case '\r', '\n':
		return keyEnter, nil
	case 0x7f, 0x08:
		return keyErase, nil
	case 0x15:
		return keyKill, nil
	case 0x1b:
		if u.r.Buffered() == 0 {
			return keyBack, nil
		}
		b1, err := u.r.ReadByte()
		if err != nil {
			return keyCancel, nil
		}
		if b1 != '[' && b1 != 'O' {
			return keyNone, nil
		}
		b2, err := u.r.ReadByte()
		if err != nil {
			return keyCancel, nil
		}
		switch b2 {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		}
		// Longer sequences, such as bracketed paste markers, end with a
		// byte from @ to ~.
		for b := b2; b < 0x40 || b > 0x7e; {
			if b, err = u.r.ReadByte(); err != nil {
				break
			}
		}
		return keyNone, nil
	}
	return int(r), nil
}

// editLine reads one line in raw mode so Esc can mean back. secret shows a
// dot per character instead of the text.
func (u *UI) editLine(question, placeholder string, secret, back bool) (string, error) {
	state, err := term.MakeRaw(u.fd)
	if err != nil {
		return "", err
	}
	defer func() { _ = term.Restore(u.fd, state) }()

	head := fmt.Sprintf("  %s %s", u.Accent("?"), u.Bold(question))
	if back {
		head += "  " + u.Dim("esc back")
	}
	fmt.Fprint(u.out, clearLine+head+"\r\n")
	var buf []rune
	draw := func() {
		line := clearLine + "  " + u.Accent("›") + " "
		switch {
		case len(buf) == 0 && placeholder != "":
			n := utf8.RuneCountInString(placeholder)
			line += u.Dim(placeholder) + fmt.Sprintf(csi+"%dD", n)
		case secret:
			line += strings.Repeat("•", min(len(buf), 40))
		default:
			line += string(buf)
		}
		fmt.Fprint(u.out, line)
	}
	done := func() {
		fmt.Fprint(u.out, clearLine+"\r"+csi+"1A"+clearLine)
	}
	draw()
	for {
		k, err := u.readKey()
		if err != nil {
			done()
			return "", err
		}
		switch k {
		case keyEnter:
			done()
			return strings.TrimSpace(string(buf)), nil
		case keyCancel:
			done()
			return "", ErrCancelled
		case keyBack:
			if back {
				done()
				return "", ErrBack
			}
		case keyErase:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case keyKill:
			buf = buf[:0]
		default:
			if k >= 0x20 && k < keyUp {
				buf = append(buf, rune(k))
			}
		}
		draw()
	}
}

func padRight(s string, n int) string {
	if v := visible(s); v < n {
		return s + strings.Repeat(" ", n-v)
	}
	return s
}

// visible counts runes, skipping colour escapes.
func visible(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := strings.IndexByte(s[i:], 'm')
			if j < 0 {
				break
			}
			i += j + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		n++
	}
	return n
}

func truncate(s string, n int) string {
	if n <= 1 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func wrap(s string, width int) []string {
	if width < 10 {
		width = 10
	}
	if visible(s) <= width {
		return []string{s}
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case visible(line)+1+visible(w) <= width:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
