package backupdelta

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func dump(rows int, seed int64) []byte {
	var b bytes.Buffer
	b.WriteString("-- dump\nDROP TABLE IF EXISTS `items`;\nCREATE TABLE `items` (id int, title text);\n\n")
	r := rand.New(rand.NewSource(seed))
	for i := 1; i <= rows; i++ {
		fmt.Fprintf(&b, "INSERT INTO `items` VALUES (%d,'title %d %d');\n", i, i, r.Intn(1000))
	}
	b.WriteString("\n-- end\n")
	return b.Bytes()
}

func roundTrip(t *testing.T, base, target []byte) []byte {
	t.Helper()
	delta := Diff(base, target)
	got, err := Apply(base, delta)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !bytes.Equal(got, target) {
		t.Fatalf("round trip differs:\nwant %q\n got %q", target, got)
	}
	return delta
}

func TestRoundTrip_EdgeCases(t *testing.T) {
	cases := map[string][2]string{
		"both empty":          {"", ""},
		"from empty":          {"", "a\nb\n"},
		"to empty":            {"a\nb\n", ""},
		"identical":           {"a\nb\nc\n", "a\nb\nc\n"},
		"no final newline":    {"a\nb", "a\nb\nc"},
		"lines moved":         {"1\n2\n3\n4\n", "3\n4\n1\n2\n"},
		"duplicate lines":     {"x\nx\nx\ny\n", "x\ny\nx\nx\nx\n"},
		"binary-ish literals": {"a\n", "a\nC 0 9\nL 3\n\x00\xff\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { roundTrip(t, []byte(c[0]), []byte(c[1])) })
	}
}

// The point of a differential: a day's few changed rows cost a few lines,
// not the whole dump.
func TestDiff_IsSmallForFewChanges(t *testing.T) {
	base := dump(20000, 1)
	lines := bytes.SplitAfter(base, []byte("\n"))
	lines[500] = []byte("INSERT INTO `items` VALUES (497,'changed');\n")
	lines = append(lines[:9000], lines[9001:]...)
	target := bytes.Join(lines, nil)
	target = append(target, []byte("INSERT INTO `items` VALUES (20001,'new');\n")...)

	delta := roundTrip(t, base, target)
	if len(delta) > 400 {
		t.Fatalf("a 3-row change produced a %d-byte delta of a %d-byte dump", len(delta), len(base))
	}
}

func TestRoundTrip_RandomEdits(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		base := dump(r.Intn(300), int64(iter))
		lines := bytes.SplitAfter(base, []byte("\n"))
		for k := r.Intn(20); k > 0 && len(lines) > 1; k-- {
			i := r.Intn(len(lines))
			switch r.Intn(3) {
			case 0:
				lines = append(lines[:i], lines[i+1:]...)
			case 1:
				lines[i] = []byte(fmt.Sprintf("edited %d\n", r.Int()))
			default:
				lines = append(lines[:i], append([][]byte{[]byte("inserted\n")}, lines[i:]...)...)
			}
		}
		roundTrip(t, base, bytes.Join(lines, nil))
	}
}

func TestApply_RejectsCorruptDeltas(t *testing.T) {
	base := []byte("a\nb\n")
	for _, bad := range []string{"C 0 5\n", "C -1 1\n", "L 10\nabc", "X 1\n", "C 0 1"} {
		if _, err := Apply(base, []byte(bad)); err == nil {
			t.Errorf("delta %q should be rejected", bad)
		}
	}
}
