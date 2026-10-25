package diff

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func apply(a, b []string, ops []Op) []string {
	var out []string
	for _, op := range ops {
		switch op.Kind {
		case Equal:
			out = append(out, a[op.A0:op.A1]...)
		case Insert:
			out = append(out, b[op.B0:op.B1]...)
		}
	}
	return out
}

func TestComputeRebuildsB(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	words := []string{"a", "b", "c", "d", "e"}
	for i := 0; i < 500; i++ {
		var a, b []string
		for j := r.Intn(30); j > 0; j-- {
			a = append(a, words[r.Intn(len(words))])
		}
		for j := r.Intn(30); j > 0; j-- {
			b = append(b, words[r.Intn(len(words))])
		}
		if got := apply(a, b, Compute(a, b)); !reflect.DeepEqual(got, b) && !(len(got) == 0 && len(b) == 0) {
			t.Fatalf("a=%v b=%v got %v", a, b, got)
		}
	}
}

func TestMarks(t *testing.T) {
	a := Lines("one\ntwo\nthree\nfour\nfive\n")
	b := Lines("one\nTWO\nthree\nnew\nfour\n")
	got := Marks(a, b)
	want := map[int]Mark{1: Modified, 3: Added, 5: Removed}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if m := Marks(nil, Lines("x\ny\n")); len(m) != 2 || m[0] != Added {
		t.Fatalf("new file %v", m)
	}
	big := strings.Repeat("line\n", 20000)
	if m := Marks(Lines(big), Lines(big+"tail\n")); len(m) != 1 {
		t.Fatalf("append to big file %v", len(m))
	}
}
