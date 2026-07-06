package query

import (
	"fmt"
	"sort"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// Stage is one segment of a pipe query. CollectFields lets the pipeline
// figure out, up front, which session fields need to be joined into the
// EventTable before any stage runs.
type Stage interface {
	CollectFields(out map[string]bool)
	Exec(t *EventTable) (*EventTable, error)
}

// --- where ---

type WhereStage struct{ Expr Expr }

func (s *WhereStage) CollectFields(out map[string]bool) { s.Expr.CollectFields(out) }

func (s *WhereStage) Exec(t *EventTable) (*EventTable, error) {
	for i := range t.Timestamps {
		if !t.Mask[i] {
			continue
		}
		v, err := s.Expr.Eval(t.RowAt(i))
		if err != nil {
			return nil, err
		}
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("where expression did not evaluate to a boolean")
		}
		if !b {
			t.Mask[i] = false
		}
	}
	return t, nil
}

// --- table ---

type TableStage struct{ Fields []string }

func (s *TableStage) CollectFields(out map[string]bool) {
	for _, f := range s.Fields {
		if f != "Timestamp" {
			out[f] = true
		}
	}
}

func (s *TableStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)
	out := &EventTable{
		Timestamps: make([]int64, len(idx)),
		Mask:       make([]bool, len(idx)),
		Columns:    make(map[string][]any, len(s.Fields)),
		End:        t.End,
	}
	for _, f := range s.Fields {
		if f == "Timestamp" {
			continue
		}
		if _, ok := t.Columns[f]; !ok {
			return nil, fmt.Errorf("unknown field: %s", f)
		}
		out.Columns[f] = make([]any, len(idx))
	}
	for newI, i := range idx {
		out.Timestamps[newI] = t.Timestamps[i]
		out.Mask[newI] = true
		for _, f := range s.Fields {
			if f == "Timestamp" {
				continue
			}
			out.Columns[f][newI] = t.Columns[f][i]
		}
	}
	return out, nil
}

// --- sort ---

type SortStage struct {
	Field string
	Desc  bool
}

func (s *SortStage) CollectFields(out map[string]bool) {
	if s.Field != "Timestamp" {
		out[s.Field] = true
	}
}

func (s *SortStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)
	key := func(i int) (float64, error) {
		if s.Field == "Timestamp" {
			return float64(t.Timestamps[i]), nil
		}
		col, ok := t.Columns[s.Field]
		if !ok {
			return 0, fmt.Errorf("unknown field: %s", s.Field)
		}
		return session.ToFloat64(col[i])
	}
	var sortErr error
	sort.SliceStable(idx, func(a, b int) bool {
		va, err := key(idx[a])
		if err != nil {
			sortErr = err
		}
		vb, err := key(idx[b])
		if err != nil {
			sortErr = err
		}
		if s.Desc {
			return va > vb
		}
		return va < vb
	})
	if sortErr != nil {
		return nil, sortErr
	}
	return materialize(t, idx), nil
}

// --- head / tail ---

type HeadStage struct{ N int }

func (s *HeadStage) CollectFields(map[string]bool) {}

func (s *HeadStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)
	if s.N < len(idx) {
		idx = idx[:s.N]
	}
	return materialize(t, idx), nil
}

type TailStage struct{ N int }

func (s *TailStage) CollectFields(map[string]bool) {}

func (s *TailStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)
	if s.N < len(idx) {
		idx = idx[len(idx)-s.N:]
	}
	return materialize(t, idx), nil
}

// --- stats ---

type AggCall struct {
	Fn    string
	Field string
	Alias string
}

type StatsStage struct {
	Aggs    []AggCall
	GroupBy []string
}

func (s *StatsStage) CollectFields(out map[string]bool) {
	for _, a := range s.Aggs {
		if a.Field != "" {
			out[a.Field] = true
		}
	}
	for _, f := range s.GroupBy {
		out[f] = true
	}
}

func (s *StatsStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)

	type group struct {
		key    []any
		rowIdx []int
	}
	groups := make(map[string]*group)
	var order []string
	for _, i := range idx {
		key := make([]any, len(s.GroupBy))
		for gi, f := range s.GroupBy {
			key[gi] = t.Columns[f][i]
		}
		keyStr := fmt.Sprint(key)
		g, ok := groups[keyStr]
		if !ok {
			g = &group{key: key}
			groups[keyStr] = g
			order = append(order, keyStr)
		}
		g.rowIdx = append(g.rowIdx, i)
	}
	if len(order) == 0 {
		// No rows; still emit an empty result table with the right columns.
		order = append(order, "")
		groups[""] = &group{}
	}

	out := &EventTable{
		Columns: make(map[string][]any, len(s.GroupBy)+len(s.Aggs)),
	}
	for _, f := range s.GroupBy {
		out.Columns[f] = nil
	}
	for _, a := range s.Aggs {
		out.Columns[a.Alias] = nil
	}

	for rowN, keyStr := range order {
		g := groups[keyStr]
		out.Timestamps = append(out.Timestamps, int64(rowN))
		out.Mask = append(out.Mask, true)
		for gi, f := range s.GroupBy {
			out.Columns[f] = append(out.Columns[f], g.key[gi])
		}
		for _, a := range s.Aggs {
			var vals []float64
			if a.Field != "" {
				col := t.Columns[a.Field]
				for _, i := range g.rowIdx {
					if v, err := session.ToFloat64(col[i]); err == nil {
						vals = append(vals, v)
					}
				}
			}
			v, err := computeAgg(a.Fn, vals, len(g.rowIdx))
			if err != nil {
				return nil, err
			}
			out.Columns[a.Alias] = append(out.Columns[a.Alias], v)
		}
	}
	return out, nil
}

// --- ranges ---

// RangesStage collapses the current mask (visible=true, filtered-out=false)
// across the full timestamp axis into contiguous [start,end) intervals,
// reusing session.FindRuns so the output matches FindBoolRanges /
// FindThresholdRanges exactly for an equivalent single-field `where` clause.
// It must be the last stage in a pipeline (enforced by Pipeline.Run).
type RangesStage struct{}

func (s *RangesStage) CollectFields(map[string]bool) {}

func (s *RangesStage) Exec(t *EventTable) (*EventTable, error) { return t, nil }

func (s *RangesStage) Collapse(t *EventTable) []session.TimeRange {
	pts := make([]session.DataPoint, len(t.Timestamps))
	for i, ts := range t.Timestamps {
		pts[i] = session.DataPoint{Timestamp: ts, Value: t.Mask[i]}
	}
	return session.FindRuns(pts, t.End, func(v any) (matches, applicable bool) {
		b, ok := v.(bool)
		return ok && b, ok
	})
}
