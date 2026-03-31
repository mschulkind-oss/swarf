package console

import (
	"bytes"
	"strings"
	"testing"
)

func TestOk(t *testing.T) {
	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = nil }()
	Ok("test message")
	if !strings.Contains(buf.String(), "✓") || !strings.Contains(buf.String(), "test message") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestWarn(t *testing.T) {
	var buf bytes.Buffer
	Stderr = &buf
	defer func() { Stderr = nil }()
	Warn("warning here")
	if !strings.Contains(buf.String(), "!") || !strings.Contains(buf.String(), "warning here") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestError(t *testing.T) {
	var buf bytes.Buffer
	Stderr = &buf
	defer func() { Stderr = nil }()
	Error("error here")
	if !strings.Contains(buf.String(), "✗") || !strings.Contains(buf.String(), "error here") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestInfo(t *testing.T) {
	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = nil }()
	Info("info line")
	if !strings.Contains(buf.String(), "info line") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestInfof(t *testing.T) {
	var buf bytes.Buffer
	Stdout = &buf
	defer func() { Stdout = nil }()
	Infof("hello %s", "world")
	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}
