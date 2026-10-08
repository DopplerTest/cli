/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package proxy

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestReadLastRunWhenNeverRun(t *testing.T) {
	run, err := ReadLastRun(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if run != nil {
		t.Fatalf("expected nil for an agent that has never run, got %+v", run)
	}
}

func TestLastRunRoundTrip(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	begun := LastRun{StartedAt: started, Command: []string{"claude", "--verbose"}}
	if err := WriteLastRun(dir, begun); err != nil {
		t.Fatal(err)
	}

	// While the sandbox is still up, the record has a start but no end.
	got, err := ReadLastRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.Equal(started) || got.EndedAt != nil || got.ExitCode != nil || got.Finished() {
		t.Fatalf("in-progress record was not preserved: %+v", got)
	}
	if !reflect.DeepEqual(got.Command, begun.Command) {
		t.Fatalf("command = %v, want %v", got.Command, begun.Command)
	}

	ended := started.Add(90 * time.Second)
	code := 130
	begun.EndedAt = &ended
	begun.ExitCode = &code
	if err := WriteLastRun(dir, begun); err != nil {
		t.Fatal(err)
	}
	got, err = ReadLastRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Finished() || !got.EndedAt.Equal(ended) || *got.ExitCode != 130 {
		t.Fatalf("finished record was not preserved: %+v", got)
	}
}

func TestReadLastRunRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(AgentLastRunPath(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLastRun(dir); err == nil {
		t.Fatal("expected an error for a corrupt record")
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := ExitCodeOf(nil); got != 0 {
		t.Fatalf("nil error should be exit 0, got %d", got)
	}
	err := exec.Command("sh", "-c", "exit 3").Run()
	if got := ExitCodeOf(err); got != 3 {
		t.Fatalf("exit 3 should be reported as 3, got %d", got)
	}
	// A failure that never produced a process still has to be reported as a failure.
	if got := ExitCodeOf(errors.New("docker: not found")); got != 1 {
		t.Fatalf("non-exec error should be exit 1, got %d", got)
	}
}

func TestIsStopExitCode(t *testing.T) {
	for code, want := range map[int]bool{0: false, 1: false, 3: false, 130: true, 143: true, 137: false} {
		if got := IsStopExitCode(code); got != want {
			t.Errorf("IsStopExitCode(%d) = %v, want %v", code, got, want)
		}
	}
}
