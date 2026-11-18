// Copyright (C) 2026 Mohsen Mirzakhani
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mirzakhany/yoga/icons"
	"github.com/mirzakhany/yoga/ui"
)

// A CSV or TSV tab shows its file as a table, the first row naming the
// columns. Editing a cell, adding or deleting a row rewrites only that
// record in the buffer, so undo, the unsaved mark and saving work as for
// any edit, and the rest of the file keeps its quoting.

// csvMode is how a CSV tab shows its file; the zero value is the table.
type csvMode int

const (
	csvTable csvMode = iota
	csvSplit
	csvSource
)

var csvModeValues = []string{"table", "split", "source"}

// csvMaxRows is how many rows the table holds; Source shows the rest.
const csvMaxRows = 50000

// csvDoc is a CSV file's parsed records and its table, read again when the
// editor's revision moves.
type csvDoc struct {
	mode  csvMode
	rev   uint64
	read  bool
	comma rune

	header  []string
	records [][]string // the rows after the header
	spans   [][2]int   // each record's bytes, header first, without its line break
	cols    int
	err     string

	table  *ui.Table
	ed     *ui.Editor // the editor shown last frame, which table edits go to
	labels []string   // the columns the table was built with
	filled uint64     // the revision the table's rows come from
	full   bool       // the table has rows from filled
	filter string
}

func isCSV(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv", ".tsv":
		return true
	}
	return false
}

func (a *App) csvDoc(path string) *csvDoc {
	if a.csvDocs == nil {
		a.csvDocs = map[string]*csvDoc{}
	}
	d := a.csvDocs[path]
	if d == nil {
		d = &csvDoc{comma: ','}
		if strings.EqualFold(filepath.Ext(path), ".tsv") {
			d.comma = '\t'
		}
		a.csvDocs[path] = d
	}
	return d
}

// parseCSV reads text's records and the bytes each one spans. On a syntax
// error it returns the records before it.
func parseCSV(text []byte, comma rune) (recs [][]string, spans [][2]int, err error) {
	starts := []int{0}
	for i, b := range text {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	r := csv.NewReader(bytes.NewReader(text))
	r.Comma = comma
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	for {
		rec, rerr := r.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			err = rerr
			break
		}
		line, col := r.FieldPos(0)
		recs = append(recs, rec)
		spans = append(spans, [2]int{starts[line-1] + col - 1, 0})
	}
	// A record ends where the next begins, less the line breaks between.
	for i := range spans {
		end := len(text)
		if i+1 < len(spans) {
			end = spans[i+1][0]
		}
		end = spans[i][0] + len(bytes.TrimRight(text[spans[i][0]:end], "\r\n"))
		spans[i][1] = end
	}
	return recs, spans, err
}

// csvRead parses ed's text into d when it changed.
func (d *csvDoc) csvRead(ed *ui.Editor) {
	if rev := ed.Revision(); d.read && rev == d.rev {
		return
	}
	d.rev, d.read = ed.Revision(), true
	recs, spans, err := parseCSV(ed.Bytes(), d.comma)
	d.err = ""
	if err != nil {
		d.err = err.Error()
	}
	d.header, d.records, d.spans, d.cols = nil, nil, spans, 0
	if len(recs) > 0 {
		d.header = recs[0]
		if len(d.header) > 0 {
			d.header[0] = strings.TrimPrefix(d.header[0], "\ufeff")
		}
		d.records = recs[1:]
	}
	for _, r := range recs {
		d.cols = max(d.cols, len(r))
	}
}

// encodeRecord is rec as one CSV record in the file's line endings, without
// its final line break.
func encodeRecord(rec []string, comma rune, eol string) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = comma
	w.UseCRLF = eol == "\r\n"
	_ = w.Write(rec)
	w.Flush()
	return strings.TrimSuffix(b.String(), eol)
}

// setCSVCell writes value to column col of data row row.
func (a *App) setCSVCell(ed *ui.Editor, d *csvDoc, row, col int, value string) {
	d.csvRead(ed)
	if row < 0 || row >= len(d.records) || col < 0 {
		return
	}
	rec := append([]string(nil), d.records[row]...)
	for len(rec) <= col {
		rec = append(rec, "")
	}
	if rec[col] == value {
		return
	}
	rec[col] = value
	sp := d.spans[row+1]
	text := ed.Bytes()
	ed.ApplyText(string(text[:sp[0]]) + encodeRecord(rec, d.comma, ed.EOL()) + string(text[sp[1]:]))
}

// deleteCSVRow removes data row row with its line break.
func (a *App) deleteCSVRow(ed *ui.Editor, d *csvDoc, row int) {
	d.csvRead(ed)
	if row < 0 || row >= len(d.records) {
		return
	}
	i := row + 1
	text := ed.Bytes()
	lo, hi := d.spans[i][0], d.spans[i][1]
	if i+1 < len(d.spans) {
		hi = d.spans[i+1][0]
	} else {
		lo = d.spans[i-1][1] // the last row takes the break before it
	}
	ed.ApplyText(string(text[:lo]) + string(text[hi:]))
}

// addCSVRow appends an empty row.
func (a *App) addCSVRow(ed *ui.Editor, d *csvDoc) {
	d.csvRead(ed)
	text, eol := string(ed.Bytes()), ed.EOL()
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += eol
	}
	ed.ApplyText(text + strings.Repeat(string(d.comma), max(d.cols-1, 0)) + eol)
}

// csvColumns are the header's names, numbered where it has none.
func (d *csvDoc) csvColumns() []string {
	labels := make([]string, d.cols)
	for i := range labels {
		if i < len(d.header) && strings.TrimSpace(d.header[i]) != "" {
			labels[i] = d.header[i]
		} else {
			labels[i] = "Column " + strconv.Itoa(i+1)
		}
	}
	return labels
}

// csvTableOf is d's table, rebuilt when the columns change and refilled
// when the text does.
func (a *App) csvTableOf(d *csvDoc) *ui.Table {
	labels := d.csvColumns()
	if d.table == nil || !slices.Equal(labels, d.labels) {
		cols := []ui.TableColumn{{ID: "#", Label: "#", Kind: ui.TableColText, Width: 56, Locked: true}}
		for i, l := range labels {
			cols = append(cols, ui.TableColumn{ID: "c" + strconv.Itoa(i), Label: l, Kind: ui.TableColEditable, Sortable: true})
		}
		cols = append(cols, ui.TableColumn{ID: "actions", Kind: ui.TableColActions, Width: 36, Locked: true})
		t := ui.NewTable(cols, []ui.TableAction{{Icon: icons.Trash2, Tooltip: "Delete row", OnClick: func(id string) {
			if n, err := strconv.Atoi(id); err == nil {
				a.deleteCSVRow(d.ed, d, n)
			}
		}}})
		t.ActionsOnHover = true
		t.Selectable = true
		t.MinHeight = 120
		t.OnCellChange = func(rowID, colID, value string) {
			n, err1 := strconv.Atoi(rowID)
			c, err2 := strconv.Atoi(strings.TrimPrefix(colID, "c"))
			if err1 == nil && err2 == nil {
				a.setCSVCell(d.ed, d, n, c, value)
			}
		}
		t.SetFilter(d.filter)
		d.table, d.labels, d.full = t, labels, false
	}
	if !d.full || d.filled != d.rev {
		rows := make([]ui.TableRow, min(len(d.records), csvMaxRows))
		for i := range rows {
			cells := make(map[string]string, len(d.records[i])+1)
			cells["#"] = strconv.Itoa(i + 1)
			for j, v := range d.records[i] {
				cells["c"+strconv.Itoa(j)] = v
			}
			rows[i] = ui.TableRow{ID: strconv.Itoa(i), Cells: cells}
		}
		d.table.SetRows(rows)
		d.filled, d.full = d.rev, true
	}
	return d.table
}

// csvModeSwitch is the source/split/table switch at the end of the
// breadcrumb row.
func (a *App) csvModeSwitch(path string) ui.View {
	d := a.csvDoc(path)
	return ui.Segmented("csv-mode",
		ui.SegmentItem{Label: "Source", Value: "source"},
		ui.SegmentItem{Label: "Split", Value: "split"},
		ui.SegmentItem{Label: "Table", Value: "table"},
	).Selected(2-int(d.mode)).OnChange(func(v string) {
		for i, m := range csvModeValues {
			if m == v {
				d.mode = csvMode(i)
			}
		}
	}).Height(22).Background(ui.TokenSurface).Border(ui.TokenSurface, 1)
}

// csvView wraps a CSV file's editor with its table.
func (a *App) csvView(c *ui.Ctx, ed *ui.Editor, editor ui.View) ui.View {
	d := a.csvDoc(ed.Path)
	if d.mode == csvSource {
		return editor
	}
	th := c.Theme()
	d.ed = ed
	d.csvRead(ed)
	t := a.csvTableOf(d)
	count := fmt.Sprintf("%d %s · %d %s", len(d.records), plural(len(d.records), "row"), d.cols, plural(d.cols, "column"))
	if len(d.records) > csvMaxRows {
		count = fmt.Sprintf("First %d of %d rows; Source shows all", csvMaxRows, len(d.records))
	}
	bar := []ui.View{
		ui.TextField("csv-filter:"+ed.Path, d.filter).Placeholder("Filter rows").IconStart(icons.Search).Width(240).
			OnChange(func(q string) {
				d.filter = q
				t.SetFilter(q)
			}),
		ui.Caption(count).Ellipsis(ui.EllipsisEnd).Shrink(1),
		ui.Spacer(),
		ui.Button("csv-add-row", ui.Text("Add row")).Ghost().IconStart(icons.Plus).Disabled(ed.ReadOnly()).
			OnClick(func() { a.addCSVRow(ed, d) }),
	}
	col := []ui.View{ui.Row(bar...).Gap(th.Spacing.S).Align(ui.AlignCenter).PaddingXY(th.Spacing.M, th.Spacing.XS)}
	if d.err != "" {
		col = append(col, ui.Alert("Could not read the whole file: "+d.err+". Rows after it are left out.", ui.AlertWarning))
	}
	col = append(col, ui.ViewOf(t).Grow(1))
	table := ui.Column(col...).Grow(1).Background(ui.TokenSurface)
	if d.mode == csvSplit {
		return ui.Splitter("csv-split", ui.Vertical, editor, table).Percents(40, 60).MinSizes(120, 160).Grow(1)
	}
	return table
}
