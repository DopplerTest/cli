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
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// The proxy log keeps at most this much: logMaxBytes in the live file and as much
// again in each of logKeep rotated files.
const (
	logMaxBytes = 10 << 20
	logKeep     = 5
)

// RotatingLog appends to a log file that survives a restart. When a write would take
// the file past its limit, the file becomes path.1 (path.1 becomes path.2, and so
// on, the oldest past keep dropped) and writing goes on in a fresh file. It is safe
// for concurrent use, since several loggers share the one file.
type RotatingLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	file     *os.File
	size     int64
	regular  bool
	closed   bool
	// rotateAt is maxBytes, or further on while the file cannot be moved, so the
	// move is not tried again on every write.
	rotateAt int64
}

// OpenProxyLog opens the proxy's log at path and marks where this run begins. The
// file holds earlier runs, and a run's first lines vary with what the engine logs
// while it starts.
func OpenProxyLog(path string) (*RotatingLog, error) {
	l, err := OpenRotatingLog(path, logMaxBytes, logKeep)
	if err != nil {
		return nil, err
	}
	log.New(l, "", log.LstdFlags).Printf("agent-proxy: proxy start, pid %d", os.Getpid())
	return l, nil
}

// OpenRotatingLog opens path for appending, creating it if needed. A path that is
// a symlink keeps pointing at the live file, since rotation renames its target.
func OpenRotatingLog(path string, maxBytes int64, keep int) (*RotatingLog, error) {
	l := &RotatingLog{path: path, maxBytes: maxBytes, keep: keep, rotateAt: maxBytes}
	if err := l.open(); err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		l.path = resolved
	}
	return l, nil
}

func (l *RotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, os.ErrClosed
	}
	// Only a regular file is moved aside. A device such as /dev/null is written to
	// as it is.
	if l.file != nil && l.regular {
		if l.movedAside() {
			l.file.Close()
			l.file = nil
			l.rotateAt = l.maxBytes
		} else if l.size > 0 && l.size+int64(len(p)) > l.rotateAt {
			l.rotate()
		}
	}
	if l.file == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *RotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return file.Close()
}

func (l *RotatingLog) open() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	l.file = file
	l.size = info.Size()
	l.regular = info.Mode().IsRegular()
	return nil
}

// movedAside reports whether another process has rotated the file, as a second
// proxy started on the same log does. The path then names a newer file, which is
// the one to write to.
func (l *RotatingLog) movedAside() bool {
	held, err := l.file.Stat()
	if err != nil {
		return true
	}
	live, err := os.Stat(l.path)
	return err != nil || !os.SameFile(held, live)
}

// rotate closes the file before renaming it, since Windows cannot rename an open
// file, and leaves the next write to open the file again. A file that cannot be
// moved stays where it is, so that write appends to it past the limit, which is
// better than lost lines.
func (l *RotatingLog) rotate() {
	l.file.Close()
	l.file = nil
	l.rotateAt = l.maxBytes
	if !l.moveAside() {
		l.rotateAt = l.size + l.maxBytes
	}
}

// moveAside moves the live file to path.0 and then every rotated file up one, and
// reports whether the live file moved. A path.0 left by a rotation that could not
// finish moves up first, so the live file never replaces it.
func (l *RotatingLog) moveAside() bool {
	if l.moveUp() != nil {
		return false
	}
	if os.Rename(l.path, l.rotated(0)) != nil {
		return false
	}
	_ = l.moveUp()
	return true
}

func (l *RotatingLog) moveUp() error {
	if _, err := os.Lstat(l.rotated(0)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	for i := l.keep; i >= 1; i-- {
		if err := os.Rename(l.rotated(i-1), l.rotated(i)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if l.keep < 1 {
		return os.Remove(l.rotated(0))
	}
	return nil
}

func (l *RotatingLog) rotated(n int) string {
	return l.path + "." + strconv.Itoa(n)
}
