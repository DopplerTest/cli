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
	"io"
	"strings"
	"testing"
)

// chunkReader hands out the input in pieces of the given sizes, so sequences straddle reads.
type chunkReader struct {
	data  []byte
	sizes []int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := len(c.data)
	if len(c.sizes) > 0 {
		n, c.sizes = c.sizes[0], c.sizes[1:]
	}
	n = min(n, len(c.data), len(p))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func forward(t *testing.T, input string, sizes ...int) (output string, copied []string) {
	t.Helper()
	var out bytes.Buffer
	err := ForwardOSC52(&out, &chunkReader{data: []byte(input), sizes: sizes}, func(text string) { copied = append(copied, text) })
	if err != nil {
		t.Fatalf("ForwardOSC52: %v", err)
	}
	return out.String(), copied
}

func TestForwardOSC52CopiesAndPassesThrough(t *testing.T) {
	input := "before\x1b]52;c;aGVsbG8=\x07after"
	out, copied := forward(t, input)
	if out != input {
		t.Fatalf("output changed: %q", out)
	}
	if len(copied) != 1 || copied[0] != "hello" {
		t.Fatalf("copied = %q, want [hello]", copied)
	}
}

func TestForwardOSC52AcceptsStringTerminator(t *testing.T) {
	input := "\x1b]52;c;d29ybGQ=\x1b\\tail"
	out, copied := forward(t, input)
	if out != input {
		t.Fatalf("output changed: %q", out)
	}
	if len(copied) != 1 || copied[0] != "world" {
		t.Fatalf("copied = %q, want [world]", copied)
	}
}

func TestForwardOSC52AcrossReads(t *testing.T) {
	input := "a\x1b]52;c;Y3Jvbm9zX3llYXI=\x07b"
	for _, sizes := range [][]int{{1, 1, 1}, {2, 3}, {7, 1, 100}, {3, 2, 2, 2, 2, 2, 2, 2}} {
		out, copied := forward(t, input, sizes...)
		if out != input {
			t.Fatalf("sizes %v: output changed: %q", sizes, out)
		}
		if len(copied) != 1 || copied[0] != "cronos_year" {
			t.Fatalf("sizes %v: copied = %q", sizes, copied)
		}
	}
}

func TestForwardOSC52LeavesOtherEscapesAlone(t *testing.T) {
	input := "\x1b[31mred\x1b[0m \x1b]0;title\x07 \x1b]5"
	out, copied := forward(t, input, 4, 1, 1, 50)
	if out != input {
		t.Fatalf("output changed: %q", out)
	}
	if len(copied) != 0 {
		t.Fatalf("copied = %q, want none", copied)
	}
}

func TestForwardOSC52IgnoresQueriesAndBadData(t *testing.T) {
	input := "\x1b]52;c;?\x07\x1b]52;c;not base64!\x07\x1b]52;c;\x07"
	out, copied := forward(t, input)
	if out != input {
		t.Fatalf("output changed: %q", out)
	}
	if len(copied) != 0 {
		t.Fatalf("copied = %q, want none", copied)
	}
}

func TestForwardOSC52FlushesAnUnfinishedSequenceAtEOF(t *testing.T) {
	input := "x\x1b]52;c;aGVs"
	out, copied := forward(t, input)
	if out != input {
		t.Fatalf("output changed: %q", out)
	}
	if len(copied) != 0 {
		t.Fatalf("copied = %q, want none", copied)
	}
}

func TestForwardOSC52PassesAnOverlongSequenceThrough(t *testing.T) {
	input := "\x1b]52;c;" + strings.Repeat("A", osc52MaxSequence+10)
	out, copied := forward(t, input, 100)
	if out != input {
		t.Fatalf("output changed (%d bytes)", len(out))
	}
	if len(copied) != 0 {
		t.Fatalf("copied = %d, want none", len(copied))
	}
}
