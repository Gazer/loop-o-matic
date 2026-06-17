package run

import (
	"context"
	"strings"
	"testing"
)

func TestBoundedWriterTruncates(t *testing.T) {
	writer := &BoundedWriter{Limit: 10}
	n, err := writer.Write([]byte("123456"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("expected 6 bytes written, got %d", n)
	}

	n, err = writer.Write([]byte("7890abc"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("expected 7 bytes written, got %d", n)
	}

	out := writer.String()
	if !strings.Contains(out, "[output truncated due to size limit]") {
		t.Fatalf("expected output to be truncated, got: %q", out)
	}
	if len(out) > 50 {
		t.Fatalf("expected output to be small, got length %d", len(out))
	}
}

func TestCommandBoundedOutput(t *testing.T) {
	// Let's run a simple command that prints some output
	res, err := Command(context.Background(), "", nil, "echo", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, "hello world") {
		t.Fatalf("expected stdout to contain 'hello world', got: %q", res.Stdout)
	}
}
