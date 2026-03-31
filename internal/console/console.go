package console

import (
	"fmt"
	"io"
	"os"
)

// Output writers — swappable for testing.
var (
	Stdout io.Writer = os.Stdout
	Stderr io.Writer = os.Stderr
)

func Ok(msg string)    { fmt.Fprintf(Stdout, "\033[32m✓\033[0m %s\n", msg) }
func Warn(msg string)  { fmt.Fprintf(Stderr, "\033[33m!\033[0m %s\n", msg) }
func Error(msg string) { fmt.Fprintf(Stderr, "\033[31m✗\033[0m %s\n", msg) }
func Info(msg string)  { fmt.Fprintln(Stdout, msg) }

func Infof(format string, a ...any) {
	fmt.Fprintf(Stdout, format+"\n", a...)
}
