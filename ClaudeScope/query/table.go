package query

import (
	"sort"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// EventTable is a columnar, forward-filled join of a session's fields onto a
// single timestamp axis (the union of every referenced field's own
// timestamps). Mask marks which rows are currently "visible" — the `where`
// stage narrows it in place (non-destructive) so that a trailing `ranges`
// stage can still see the full timestamp axis and collapse it into
// contiguous true/false runs, matching FindBoolRanges/FindThresholdRanges.
// Every other stage (table/sort/head/tail/stats) materializes a brand new
// EventTable from the currently-visible rows.
type EventTable struct {
	Timestamps []int64
	Mask       []bool
	Columns    map[string][]any // field -> values, parallel to Timestamps
	End        int64            // window end, used to close a run still open at the last row
}

// RowAt returns row i (including the pseudo-field "Timestamp") regardless of
// its mask state.
func (t *EventTable) RowAt(i int) Row {
	row := make(Row, len(t.Columns)+1)
	row["Timestamp"] = t.Timestamps[i]
	for f, col := range t.Columns {
		row[f] = col[i]
	}
	return row
}

// Rows returns visible rows as plain maps, suitable for JSON serialization.
func (t *EventTable) Rows() []map[string]any {
	out := make([]map[string]any, 0, len(t.Timestamps))
	for i := range t.Timestamps {
		if !t.Mask[i] {
			continue
		}
		out = append(out, t.RowAt(i))
	}
	return out
}

// visibleIndices returns the indices of currently-masked-in rows, in order.
func visibleIndices(t *EventTable) []int {
	idx := make([]int, 0, len(t.Timestamps))
	for i, ok := range t.Mask {
		if ok {
			idx = append(idx, i)
		}
	}
	return idx
}

// materialize builds a brand new EventTable containing exactly the rows at
// idx (in that order), all masked visible. Used by stages that reorder or
// truncate rows (sort/head/tail).
func materialize(t *EventTable, idx []int) *EventTable {
	out := &EventTable{
		Timestamps: make([]int64, len(idx)),
		Mask:       make([]bool, len(idx)),
		Columns:    make(map[string][]any, len(t.Columns)),
		End:        t.End,
	}
	for f := range t.Columns {
		out.Columns[f] = make([]any, len(idx))
	}
	for newI, i := range idx {
		out.Timestamps[newI] = t.Timestamps[i]
		out.Mask[newI] = true
		for f, col := range t.Columns {
			out.Columns[f][newI] = col[i]
		}
	}
	return out
}

// BuildEventTable joins the given fields from sess into a single forward-filled
// EventTable covering [start, end] (same 0/negative-offset semantics as
// DataSession.GetRanges).
func BuildEventTable(sess session.DataSession, fields []string, start, end int64) (*EventTable, error) {
	_, sessEnd, err := sess.TimeRange()
	if err != nil {
		return nil, err
	}
	windowEnd := end
	if windowEnd <= 0 {
		windowEnd = sessEnd + end // end==0 -> sessEnd; negative -> offset from end
	}

	ranged, err := sess.GetRanges(fields, start, end)
	if err != nil {
		return nil, err
	}

	// Only fields whose first in-window point is missing/late need a
	// carry-in seed. start<=0 means "from the beginning of the log", where
	// there is nothing before the window to seed from anyway.
	seed := map[string]*session.DataPoint{}
	if start > 0 {
		if seedVals, err := sess.GetValues(fields, start); err == nil {
			seed = seedVals
		}
	}

	type point struct {
		ts    int64
		field string
		value any
	}
	var all []point
	for _, f := range fields {
		pts := ranged[f]
		if sp, ok := seed[f]; ok && (len(pts) == 0 || pts[0].Timestamp > start) {
			all = append(all, point{ts: start, field: f, value: sp.Value})
		}
		for _, dp := range pts {
			all = append(all, point{ts: dp.Timestamp, field: f, value: dp.Value})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ts < all[j].ts })

	t := &EventTable{
		Columns: make(map[string][]any, len(fields)),
		End:     windowEnd,
	}
	current := make(map[string]any, len(fields))
	var lastTS int64
	haveRow := false
	flush := func(ts int64) {
		t.Timestamps = append(t.Timestamps, ts)
		t.Mask = append(t.Mask, true)
		for _, f := range fields {
			t.Columns[f] = append(t.Columns[f], current[f])
		}
	}
	for _, p := range all {
		if haveRow && p.ts != lastTS {
			flush(lastTS)
		}
		current[p.field] = p.value
		lastTS = p.ts
		haveRow = true
	}
	if haveRow {
		flush(lastTS)
	}
	return t, nil
}
