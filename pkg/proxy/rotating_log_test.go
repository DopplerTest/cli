/*
Copyright © 2026 Doppler <support@doppler.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func writeLog(t *testing.T, l *RotatingLog, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if _, err := l.Write([]byte(line)); err != nil {
			t.Fatalf("write %q: %v", line, err)
		}
	}
}

func openLog(t *testing.T, path string, maxBytes int64, keep int) *RotatingLog {
	t.Helper()
	l, err := OpenRotatingLog(path, maxBytes, keep)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestRotatingLogAppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	first := openLog(t, path, logMaxBytes, logKeep)
	writeLog(t, first, "first run\n")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	writeLog(t, openLog(t, path, logMaxBytes, logKeep), "second run\n")

	if got := readLog(t, path); got != "first run\nsecond run\n" {
		t.Fatalf("the earlier run's lines should be kept; got %q", got)
	}
	if _, err := os.Stat(path + ".1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("nothing should rotate under the limit; got %v", err)
	}
}

func TestRotatingLogRotatesPastLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, 10, 5)

	writeLog(t, l, "aaaa\n", "bbbb\n")
	if got := readLog(t, path); got != "aaaa\nbbbb\n" {
		t.Fatalf("a write that reaches the limit exactly stays in the file; got %q", got)
	}

	writeLog(t, l, "cccc\n")
	writeLog(t, l, "ddddddddd\n", "eeee\n")

	want := map[string]string{
		path:        "eeee\n",
		path + ".1": "ddddddddd\n",
		path + ".2": "cccc\n",
		path + ".3": "aaaa\nbbbb\n",
	}
	for p, w := range want {
		if got := readLog(t, p); got != w {
			t.Errorf("%s = %q, want %q", filepath.Base(p), got, w)
		}
	}
}

func TestRotatingLogKeepsWriteOverLimitWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, 10, 5)
	long := strings.Repeat("z", 25) + "\n"

	writeLog(t, l, long, "bbbb\n")

	want := map[string]string{path: "bbbb\n", path + ".1": long}
	for p, w := range want {
		if got := readLog(t, p); got != w {
			t.Errorf("%s = %q, want %q", filepath.Base(p), got, w)
		}
	}
	if _, err := os.Stat(path + ".2"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an empty file should never rotate; got %v", err)
	}
}

func TestRotatingLogCountsExistingFileTowardLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(path, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeLog(t, openLog(t, path, 10, 5), "later\n")

	if got := readLog(t, path+".1"); got != "earlier\n" {
		t.Fatalf("the reopened file should rotate once the write passes the limit; got %q", got)
	}
	if got := readLog(t, path); got != "later\n" {
		t.Fatalf("the write should land in a fresh file; got %q", got)
	}
}

func TestRotatingLogDropsOldestPastKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, 5, 2)

	writeLog(t, l, "aaaa\n", "bbbb\n", "cccc\n", "dddd\n")

	want := map[string]string{path: "dddd\n", path + ".1": "cccc\n", path + ".2": "bbbb\n"}
	for p, w := range want {
		if got := readLog(t, p); got != w {
			t.Errorf("%s = %q, want %q", filepath.Base(p), got, w)
		}
	}
	if _, err := os.Stat(path + ".3"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no file past the keep count should remain; got %v", err)
	}
}

func TestRotatingLogFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix file modes")
	}
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, 5, 2)

	writeLog(t, l, "aaaa\n", "bbbb\n", "cccc\n")

	for _, p := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}

func TestRotatingLogKeepsConcurrentLinesWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	const writers, linesEach = 20, 50
	l := openLog(t, path, 1000, writers*linesEach)

	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range linesEach {
				if _, err := fmt.Fprintf(l, "writer %02d line %02d\n", w, i); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()

	matches, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, p := range matches {
		lines = append(lines, strings.Split(strings.TrimSuffix(readLog(t, p), "\n"), "\n")...)
	}
	if len(lines) != writers*linesEach {
		t.Fatalf("got %d lines across %d files, want %d", len(lines), len(matches), writers*linesEach)
	}
	for _, line := range lines {
		if len(line) != len("writer 00 line 00") {
			t.Fatalf("a line was split or interleaved: %q", line)
		}
	}
}

func TestRotatingLogWritesAgainAfterItsDirectoryReturns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proxy")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "proxy.log")
	l := openLog(t, path, 10, logKeep)
	writeLog(t, l, "aaaa\n")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("bbbbbbbb\n")); err == nil {
		t.Fatal("a write should fail while the directory is gone")
	}

	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, l, "cccc\n")
	if got := readLog(t, path); got != "cccc\n" {
		t.Fatalf("writing should go on once the file can be opened again; got %q", got)
	}
}

func TestRotatingLogRotatesTheTargetOfASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symlink needs a privilege on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.log")
	link := filepath.Join(dir, "proxy.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	l := openLog(t, link, 10, logKeep)
	writeLog(t, l, "aaaaaaaa\n", "bbbbbbbb\n")

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		t.Fatal("rotation should leave the symlink in place")
	}
	if got := readLog(t, link); got != "bbbbbbbb\n" {
		t.Fatalf("the symlink should lead to the live file; got %q", got)
	}
	if got := readLog(t, target+".1"); got != "aaaaaaaa\n" {
		t.Fatalf("the earlier lines should move beside the target; got %q", got)
	}
}

func TestRotatingLogFollowsARotationByAnotherWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	running := openLog(t, path, 40, logKeep)
	writeLog(t, running, "running proxy 1\n")

	// A second writer on the same file takes it past the limit and rotates it.
	writeLog(t, openLog(t, path, 40, logKeep), "a second proxy on this log\n")

	writeLog(t, running, "running proxy 2\n")

	if got := readLog(t, path); got != "a second proxy on this log\nrunning proxy 2\n" {
		t.Fatalf("the first writer should move on to the live file; got %q", got)
	}
	if got := readLog(t, path+".1"); got != "running proxy 1\n" {
		t.Fatalf("the rotated file should hold the earlier lines; got %q", got)
	}
	if _, err := os.Stat(path + ".2"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the first writer should not rotate the newer file aside; got %v", err)
	}
}

func TestRotatingLogFinishesARotationThatCouldNotMoveAnOlderFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(path+".2", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory with something in it where an older file moves to makes that
	// rename fail.
	if err := os.MkdirAll(filepath.Join(path+".3", "held"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := openLog(t, path, 10, 3)
	writeLog(t, l, "aaaaaaaa\n", "bbbbbbbb\n", "cccccccc\n")

	if got := readLog(t, path); got != "bbbbbbbb\ncccccccc\n" {
		t.Fatalf("lines should go on in the live file while older files cannot move; got %q", got)
	}
	if got := readLog(t, path+".0"); got != "aaaaaaaa\n" {
		t.Fatalf("the file moved aside should be kept; got %q", got)
	}

	if err := os.RemoveAll(path + ".3"); err != nil {
		t.Fatal(err)
	}
	writeLog(t, l, "dddddddd\n")

	for name, want := range map[string]string{
		path:        "dddddddd\n",
		path + ".1": "bbbbbbbb\ncccccccc\n",
		path + ".2": "aaaaaaaa\n",
		path + ".3": "older\n",
	} {
		if got := readLog(t, name); got != want {
			t.Fatalf("%s should hold %q once the rotation can finish; got %q", filepath.Base(name), want, got)
		}
	}
}

func TestRotatingLogRotatesAtTheLimitAfterAnOlderFileCouldNotMove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	if err := os.WriteFile(path+".1", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path+".2", "held"), 0o700); err != nil {
		t.Fatal(err)
	}
	l := openLog(t, path, 10, 2)
	writeLog(t, l, "aaaaaaaa\n", "bbbbbbbb\n")
	if err := os.RemoveAll(path + ".2"); err != nil {
		t.Fatal(err)
	}

	writeLog(t, l, "cccccccc\n")

	if got := readLog(t, path); got != "cccccccc\n" {
		t.Fatalf("the fresh file should rotate at its usual limit; got %q", got)
	}
}

func TestOpenProxyLogMarksEachStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	first, err := OpenProxyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	writeLog(t, first, "first run\n")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenProxyLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	lines := strings.Split(strings.TrimSuffix(readLog(t, path), "\n"), "\n")
	marker := fmt.Sprintf("agent-proxy: proxy start, pid %d", os.Getpid())
	if len(lines) != 3 || !strings.HasSuffix(lines[0], marker) || lines[1] != "first run" || !strings.HasSuffix(lines[2], marker) {
		t.Fatalf("each start should add its own line after the earlier run; got %q", lines)
	}
}

func TestRotatingLogWithNoKeepDropsTheRotatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, 10, 0)

	writeLog(t, l, "aaaaaaaa\n", "bbbbbbbb\n")

	if got := readLog(t, path); got != "bbbbbbbb\n" {
		t.Fatalf("writing should go on in a fresh file; got %q", got)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("no rotated file should be kept; got %d files", len(entries))
	}
}

func TestRotatingLogRefusesAWriteAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	l := openLog(t, path, logMaxBytes, logKeep)
	writeLog(t, l, "before close\n")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Write([]byte("after close\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("a write after close should be refused; got %v", err)
	}
	if got := readLog(t, path); got != "before close\n" {
		t.Fatalf("a closed log should not be opened again; got %q", got)
	}
}
