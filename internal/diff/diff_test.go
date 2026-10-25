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

func TestHunksApply(t *testing.T) {
	a := Lines("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	b := Lines("1\nTWO\n3\n4\n5\n6\n7\n8\nNINE\n10\n")
	hs := Hunks(a, b, 1)
	if len(hs) != 2 {
		t.Fatalf("hunks %+v", hs)
	}
	if got := Apply(a, b, hs, []bool{true, false}, true); got != "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\n" {
		t.Fatalf("partial %q", got)
	}
	if got := Apply(a, b, hs, []bool{true, true}, true); got != strings.Join(b, "\n")+"\n" {
		t.Fatalf("all %q", got)
	}
	if hs := Hunks(a, b, 4); len(hs) != 1 {
		t.Fatalf("close hunks should merge: %+v", hs)
	}
}
