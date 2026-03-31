package console

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Output writers — swappable for testing.
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

// ANSI color codes.
const (
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Italic    = "\033[3m"
	Green     = "\033[32m"
	Yellow    = "\033[33m"
	Red       = "\033[31m"
	Cyan      = "\033[36m"
	Blue      = "\033[34m"
	Magenta   = "\033[35m"
	BoldCyan  = "\033[1;36m"
	BoldGreen = "\033[1;32m"
	BoldWhite = "\033[1;37m"
)

// IsTTY returns true if stdout is a terminal.
func IsTTY() bool {
	if f, ok := Stdout.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// Color wraps text in an ANSI color code, respecting TTY detection.
func Color(code, text string) string {
	if !IsTTY() {
		return text
	}
	return code + text + Reset
}

func Ok(msg string)    { fmt.Fprintf(Stdout, "%s %s\n", Color(Green, "✓"), msg) }
func Warn(msg string)  { fmt.Fprintf(Stderr, "%s %s\n", Color(Yellow, "!"), msg) }
func Error(msg string) { fmt.Fprintf(Stderr, "%s %s\n", Color(Red, "✗"), msg) }
func Info(msg string)  { fmt.Fprintln(Stdout, msg) }

func Infof(format string, a ...any) {
	fmt.Fprintf(Stdout, format+"\n", a...)
}

// Header prints a bold section header.
func Header(msg string) {
	fmt.Fprintf(Stdout, "%s\n", Color(Bold, msg))
}

// Hint prints a dim hint line.
func Hint(msg string) {
	fmt.Fprintf(Stdout, "%s\n", Color(Dim, msg))
}
