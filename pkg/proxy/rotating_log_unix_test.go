//go:build unix

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
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRotatingLogLeavesANamedPipeInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Opening a pipe for writing waits for a reader.
	reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	l := openLog(t, path, 10, logKeep)

	writeLog(t, l, "aaaaaaaa\n", "bbbbbbbb\n")

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&fs.ModeNamedPipe == 0 {
		t.Fatal("a file that is not a regular file should not be moved aside")
	}
	if _, err := os.Stat(path + ".1"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("nothing should rotate; got %v", err)
	}
}

func TestRotatingLogWritesOnWhenNothingCanMove(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can rename in a directory without write permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.log")
	if err := os.WriteFile(path+".1", []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := openLog(t, path, 10, 2)
	writeLog(t, l, "aaaaaaaa\n")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	writeLog(t, l, "bbbbbbbb\n", "cccccccc\n", "dddddddd\n")

	if got := readLog(t, path); got != "aaaaaaaa\nbbbbbbbb\ncccccccc\ndddddddd\n" {
		t.Fatalf("lines should go on in the file that could not move; got %q", got)
	}
	if got := readLog(t, path+".1"); got != "older\n" {
		t.Fatalf("the older file should stay where it is; got %q", got)
	}
}

func TestRotatingLogRotatesAtTheLimitInAFileAnotherWriterStarted(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can rename in a directory without write permission")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "proxy.log")
	l := openLog(t, path, 10, 2)
	writeLog(t, l, "aaaaaaaa\n")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	writeLog(t, l, "bbbbbbbb\n")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Another writer rotates the file, so the path names a fresh one.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}

	writeLog(t, l, "cccccccc\n", "dddddddd\n")

	if got := readLog(t, path); got != "dddddddd\n" {
		t.Fatalf("the fresh file should rotate at its usual limit; got %q", got)
	}
}
