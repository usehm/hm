// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCSVSpans(t *testing.T) {
	text := "a,b\r\n\"x\ny\",2\r\n\r\n  q,3\n"
	recs, spans, err := parseCSV([]byte(text), ',')
	if err != nil || len(recs) != 3 {
		t.Fatalf("%v %v", recs, err)
	}
	for i, want := range []string{"a,b", "\"x\ny\",2", "  q,3"} {
		if got := text[spans[i][0]:spans[i][1]]; got != want {
			t.Errorf("record %d spans %q, want %q", i, got, want)
		}
	}
}

func openCSV(t *testing.T, text string) (*harness, string) {
	t.Helper()
	root := project(t)
	path := filepath.Join(root, "data.csv")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, root, false)
	h.app.openFile(path)
	h.frame()
	return h, path
}

// Editing a cell rewrites only its record; the rest keeps its quoting.
func TestCSVTableEdits(t *testing.T) {
	h, path := openCSV(t, "name,age\r\n\"Ann\",30\r\nBob,41\r\n")
	d := h.app.csvDoc(path)
	if d.table == nil || len(d.table.Rows) != 2 || d.table.Rows[1].Cells["c0"] != "Bob" {
		t.Fatalf("table %+v", d.table)
	}
	ed := h.app.curEditor()
	d.table.OnCellChange("1", "c1", "42, really")
	if got := string(ed.Bytes()); got != "name,age\r\n\"Ann\",30\r\nBob,\"42, really\"\r\n" {
		t.Fatalf("after edit: %q", got)
	}
	h.frame()
	if d.table.Rows[1].Cells["c1"] != "42, really" {
		t.Fatal("the table should show the edit")
	}
	h.app.addCSVRow(ed, d)
	if got := string(ed.Bytes()); got != "name,age\r\n\"Ann\",30\r\nBob,\"42, really\"\r\n,\r\n" {
		t.Fatalf("after add: %q", got)
	}
	h.app.deleteCSVRow(ed, d, 0)
	if got := string(ed.Bytes()); got != "name,age\r\nBob,\"42, really\"\r\n,\r\n" {
		t.Fatalf("after delete: %q", got)
	}
	h.app.deleteCSVRow(ed, d, 1)
	if got := string(ed.Bytes()); got != "name,age\r\nBob,\"42, really\"\r\n" {
		t.Fatalf("after deleting the last row: %q", got)
	}
	if !ed.Modified() {
		t.Fatal("the buffer should be unsaved")
	}
}

// Source and split draw; a stray quote reads leniently, to the end.
func TestCSVModesAndErrors(t *testing.T) {
	h, path := openCSV(t, "a,b\n1,2\n")
	d := h.app.csvDoc(path)
	d.mode = csvSplit
	h.frame()
	d.mode = csvSource
	h.frame()
	ed := h.app.curEditor()
	ed.SetText("a,b\n1,\"unterminated\n")
	d.mode = csvTable
	h.frame()
	if len(d.records) != 1 || d.records[0][1] != "unterminated\n" || d.err != "" {
		t.Fatalf("records %q err %q", d.records, d.err)
	}
}
