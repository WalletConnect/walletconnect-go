package trace

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSpan(t *testing.T) {
	var buf bytes.Buffer
	tr := New(slog.New(slog.NewTextHandler(&buf, nil)), true)
	s := tr.Start("request", "method", "personal_sign")
	From(With(context.Background(), s)).Step("relay send", "id", 7)
	s.End(errors.New("boom"))

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines:\n%s", len(lines), buf.String())
	}
	for i, want := range []string{"step=start", `step="relay send"`, "step=failed"} {
		if !strings.Contains(lines[i], "trace="+s.ID()) || !strings.Contains(lines[i], want) {
			t.Errorf("line %d = %s, want trace=%s and %s", i, lines[i], s.ID(), want)
		}
	}
	if !strings.Contains(lines[2], "err=boom") {
		t.Errorf("end line lacks error: %s", lines[2])
	}
}

func TestOff(t *testing.T) {
	tr := New(slog.Default(), false)
	s := tr.Start("x")
	s.Step("y")
	s.End(nil)
	tr.Event("z")
	if s != nil || s.ID() != "" || From(With(context.Background(), s)) != nil {
		t.Fatal("tracing off should produce nil spans")
	}
}
