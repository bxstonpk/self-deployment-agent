// Package backupdelta computes and applies the differential half of the
// database backup policy (DEC-025, decided 2026-10-23): a full dump on
// Sunday, and on every other day only what changed since that dump.
//
// The unit is a line. Both engines' dumps put one row per line here —
// pg_dump's COPY blocks always do, and mysqldump does with
// --skip-extended-insert — so a changed row changes one line and an
// unchanged table costs a single copy instruction, whatever its size.
//
// A delta is a sequence of instructions that rebuild the target from the
// base:
//
//	C <first> <count>\n          copy <count> lines of the base, from line <first>
//	L <nbytes>\n<nbytes bytes>   insert these bytes literally
//
// Literal data is length-prefixed, so it may contain anything, including
// newlines and a final line with no newline.
package backupdelta

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// maxCandidates bounds how many earlier occurrences of a line are tried
// as the start of a copy. Dumps repeat a few lines constantly (blank
// lines, statement terminators); trying every occurrence of those would be
// quadratic for no benefit.
const maxCandidates = 8

func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	return bytes.SplitAfter(b, []byte("\n"))
}

type writer struct {
	out     bytes.Buffer
	literal bytes.Buffer
}

func (w *writer) flushLiteral() {
	if w.literal.Len() == 0 {
		return
	}
	fmt.Fprintf(&w.out, "L %d\n", w.literal.Len())
	w.out.Write(w.literal.Bytes())
	w.literal.Reset()
}

func (w *writer) copy(first, count int) {
	w.flushLiteral()
	fmt.Fprintf(&w.out, "C %d %d\n", first, count)
}

// Diff returns a delta that rebuilds target from base.
func Diff(base, target []byte) []byte {
	baseLines := splitLines(base)
	targetLines := splitLines(target)
	index := make(map[string][]int, len(baseLines))
	for i, line := range baseLines {
		if s := string(line); len(index[s]) < maxCandidates {
			index[s] = append(index[s], i)
		}
	}

	w := &writer{}
	next := 0 // the base line right after the last copy: tried first, since dumps change little
	for j := 0; j < len(targetLines); {
		bestStart, bestLen := -1, 0
		extend := func(i int) {
			n := 0
			for i+n < len(baseLines) && j+n < len(targetLines) && bytes.Equal(baseLines[i+n], targetLines[j+n]) {
				n++
			}
			if n > bestLen {
				bestStart, bestLen = i, n
			}
		}
		if next < len(baseLines) && bytes.Equal(baseLines[next], targetLines[j]) {
			extend(next)
		}
		for _, i := range index[string(targetLines[j])] {
			if i != next {
				extend(i)
			}
		}
		if bestLen == 0 {
			w.literal.Write(targetLines[j])
			j++
			continue
		}
		w.copy(bestStart, bestLen)
		j += bestLen
		next = bestStart + bestLen
	}
	w.flushLiteral()
	return w.out.Bytes()
}

// Apply rebuilds the target a delta describes from its base.
func Apply(base, delta []byte) ([]byte, error) {
	baseLines := splitLines(base)
	var out bytes.Buffer
	r := bufio.NewReader(bytes.NewReader(delta))
	for {
		header, err := r.ReadString('\n')
		if errors.Is(err, io.EOF) && header == "" {
			return out.Bytes(), nil
		}
		if err != nil {
			return nil, fmt.Errorf("backup delta: truncated instruction")
		}
		var op string
		var a, b int
		switch header[0] {
		case 'C':
			if _, err := fmt.Sscanf(header, "%s %d %d\n", &op, &a, &b); err != nil || a < 0 || b <= 0 || a+b > len(baseLines) {
				return nil, fmt.Errorf("backup delta: bad copy %q for a base of %d lines", header, len(baseLines))
			}
			for _, line := range baseLines[a : a+b] {
				out.Write(line)
			}
		case 'L':
			n, err := strconv.Atoi(header[2 : len(header)-1])
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("backup delta: bad literal %q", header)
			}
			if _, err := io.CopyN(&out, r, int64(n)); err != nil {
				return nil, fmt.Errorf("backup delta: literal shorter than declared")
			}
		default:
			return nil, fmt.Errorf("backup delta: unknown instruction %q", header)
		}
	}
}
