package maintenance

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReaperRunsEveryTaskEachIntervalAndStopsOnCancel(t *testing.T) {
	var first, second atomic.Int32
	reaper := NewReaper(slog.New(slog.DiscardHandler), 5*time.Millisecond,
		Task{Name: "first", Run: func(context.Context) (int64, error) { first.Add(1); return 1, nil }},
		Task{Name: "second", Run: func(context.Context) (int64, error) { second.Add(1); return 0, nil }},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for (first.Load() < 2 || second.Load() < 2) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if first.Load() < 2 || second.Load() < 2 {
		t.Errorf("tasks ran first=%d second=%d times, want at least 2 each", first.Load(), second.Load())
	}
}

func TestReaperLogsAFailingTaskAndKeepsGoing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	var after atomic.Int32
	reaper := NewReaper(logger, 5*time.Millisecond,
		Task{Name: "broken", Run: func(context.Context) (int64, error) { return 0, errors.New("table locked") }},
		Task{Name: "after", Run: func(context.Context) (int64, error) { after.Add(1); return 0, nil }},
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for after.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	if after.Load() < 1 {
		t.Fatal("task after the failing one never ran")
	}
	out := buf.String()
	if !strings.Contains(out, "reaper task failed") || !strings.Contains(out, "task=broken") || !strings.Contains(out, "table locked") {
		t.Errorf("failure was not logged with task name and error:\n%s", out)
	}
}

func TestReaperRunsNothingBeforeTheFirstTick(t *testing.T) {
	var runs atomic.Int32
	reaper := NewReaper(slog.New(slog.DiscardHandler), time.Hour,
		Task{Name: "t", Run: func(context.Context) (int64, error) { runs.Add(1); return 0, nil }},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reaper.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	if runs.Load() != 0 {
		t.Errorf("task ran %d times before the first tick", runs.Load())
	}
}
