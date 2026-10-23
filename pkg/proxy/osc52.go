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
	"bytes"
	"encoding/base64"
	"io"
	"strings"
)

// A program in the sandbox with no clipboard tool of its own, Claude Code among them, copies
// text by writing an OSC 52 escape sequence to its terminal: ESC ] 52 ; <selection> ; <base64>
// ended by BEL or ESC \. Terminal.app ignores the sequence, so nothing reaches the Mac
// clipboard. The scanner below watches the sandbox's output for those sequences on the way to
// the real terminal and hands their text to the caller, which copies it on the host.

// osc52MaxSequence caps how much of an unfinished sequence is held back before it is passed
// through unexamined, so a stray ESC ] can never stall the output.
const osc52MaxSequence = 1 << 20

// ForwardOSC52 copies r to w unchanged and calls onCopy with the text of every OSC 52
// clipboard write it passes. A query (data "?") and undecodable data are passed through
// without a call.
func ForwardOSC52(w io.Writer, r io.Reader, onCopy func(text string)) error {
	s := &osc52Scanner{w: w, onCopy: onCopy}
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if werr := s.write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if ferr := s.flush(); ferr != nil {
				return ferr
			}
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type osc52Scanner struct {
	w      io.Writer
	onCopy func(string)
	// held is the start of a sequence that may be an OSC 52 write, kept until it completes or
	// turns out to be something else.
	held []byte
}

var osc52Prefix = []byte("\x1b]52;")

// write passes chunk through, holding back from the last ESC that could still begin an OSC 52
// sequence until the rest arrives.
func (s *osc52Scanner) write(chunk []byte) error {
	data := chunk
	if len(s.held) > 0 {
		data = append(s.held, chunk...)
		s.held = nil
	}
	for len(data) > 0 {
		esc := bytes.IndexByte(data, 0x1b)
		if esc < 0 {
			return s.emit(data)
		}
		if err := s.emit(data[:esc]); err != nil {
			return err
		}
		data = data[esc:]
		rest, done, isOSC52 := s.cut(data)
		if !done {
			if len(data) > osc52MaxSequence || !couldBeOSC52(data) {
				// Not a clipboard write, or too long to be one worth holding: pass it on.
				if err := s.emit(data); err != nil {
					return err
				}
				return nil
			}
			s.held = append([]byte(nil), data...)
			return nil
		}
		seq := data[:len(data)-len(rest)]
		if isOSC52 {
			s.copy(seq)
		}
		if err := s.emit(seq); err != nil {
			return err
		}
		data = rest
	}
	return nil
}

// cut finds the end of the sequence at the start of data. done is false while the terminator
// has not arrived; isOSC52 says whether the finished sequence is a clipboard write.
func (s *osc52Scanner) cut(data []byte) (rest []byte, done bool, isOSC52 bool) {
	if !bytes.HasPrefix(data, osc52Prefix) {
		if couldBeOSC52(data) {
			return nil, false, false
		}
		// Some other escape: hand the ESC through on its own and carry on after it.
		return data[1:], true, false
	}
	for i := len(osc52Prefix); i < len(data); i++ {
		switch data[i] {
		case 0x07:
			return data[i+1:], true, true
		case 0x1b:
			if i+1 < len(data) {
				if data[i+1] == '\\' {
					return data[i+2:], true, true
				}
				// An ESC that starts something else ends this sequence without a copy.
				return data[i:], true, false
			}
			return nil, false, false
		}
	}
	return nil, false, false
}

// couldBeOSC52 reports whether data, starting at ESC, is a prefix of "ESC ] 5 2 ;" or longer.
func couldBeOSC52(data []byte) bool {
	n := min(len(data), len(osc52Prefix))
	return bytes.Equal(data[:n], osc52Prefix[:n])
}

func (s *osc52Scanner) copy(seq []byte) {
	body := seq[len(osc52Prefix):]
	body = bytes.TrimSuffix(body, []byte{0x07})
	body = bytes.TrimSuffix(body, []byte("\x1b\\"))
	// The selection list (c, p, s, …) comes first, then the data.
	sep := bytes.IndexByte(body, ';')
	if sep < 0 {
		return
	}
	data := strings.TrimSpace(string(body[sep+1:]))
	if data == "" || data == "?" {
		return
	}
	text, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		if text, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(data, "=")); err != nil {
			return
		}
	}
	if s.onCopy != nil {
		s.onCopy(string(text))
	}
}

func (s *osc52Scanner) emit(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	_, err := s.w.Write(b)
	return err
}

func (s *osc52Scanner) flush() error {
	held := s.held
	s.held = nil
	return s.emit(held)
}
