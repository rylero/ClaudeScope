package query

import (
	"fmt"
	"regexp"
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

// fieldProducer is implemented by stages that create new columns mid-pipeline
// (eval, rex, stats). The pipeline uses this to distinguish real session
// fields — which must be joined from the log — from computed columns produced
// downstream, so it never asks the session for a name that eval/rex will make.
type fieldProducer interface {
	ProducedFields() []string
}

// --- where ---

type WhereStage struct{ Expr Expr }

func (s *WhereStage) CollectFields(out map[string]bool) { s.Expr.CollectFields(out) }

func (s *WhereStage) Exec(t *EventTable) (*EventTable, error) {
	for i := range t.Timestamps {
		if !t.Mask[i] {
			continue
		}
		b, err := evalBool(s.Expr, t.RowAt(i), "where")
		if err != nil {
			return nil, err
		}
		if !b {
			t.Mask[i] = false
		}
	}
	return t, nil
}

// --- eval ---

// EvalStage computes a scalar expression per row and stores it as a new (or
// overwritten) column. It runs over every row, not just the visible ones, so a
// later `ranges` stage still sees the full timestamp axis.
type EvalStage struct {
	Target string
	Expr   Expr
}

func (s *EvalStage) CollectFields(out map[string]bool) { s.Expr.CollectFields(out) }
func (s *EvalStage) ProducedFields() []string          { return []string{s.Target} }

func (s *EvalStage) Exec(t *EventTable) (*EventTable, error) {
	col := make([]any, len(t.Timestamps))
	for i := range t.Timestamps {
		v, err := s.Expr.Eval(t.RowAt(i))
		if err != nil {
			return nil, err
		}
		col[i] = v
	}
	t.Columns[s.Target] = col
	return t, nil
}

// --- rex ---

// RexStage applies a regular expression with named capture groups to a string
// column, storing each group as a new column. Rows that do not match get nil
// for the group columns.
type RexStage struct {
	Field  string
	Re     *regexp.Regexp
	Groups []string
}

func (s *RexStage) CollectFields(out map[string]bool) { out[s.Field] = true }
func (s *RexStage) ProducedFields() []string          { return s.Groups }

func (s *RexStage) Exec(t *EventTable) (*EventTable, error) {
	src, ok := t.Columns[s.Field]
	if !ok {
		return nil, fmt.Errorf("unknown field: %s", s.Field)
	}
	cols := make(map[string][]any, len(s.Groups))
	for _, g := range s.Groups {
		cols[g] = make([]any, len(t.Timestamps))
	}
	names := s.Re.SubexpNames()
	for i := range t.Timestamps {
		if src[i] == nil {
			continue
		}
		m := s.Re.FindStringSubmatch(fmt.Sprint(src[i]))
		if m == nil {
			continue
		}
		for gi, name := range names {
			if name == "" {
				continue
			}
			cols[name][i] = m[gi]
		}
	}
	for g, c := range cols {
		t.Columns[g] = c
	}
	return t, nil
}

// --- lookup ---

// lookupOutput is one `output <csv column> [as <alias>]` clause: a
// precomputed key-value -> output-value map loaded from the CSV at parse
// time, plus the EventTable column name it gets stored under.
type lookupOutput struct {
	alias string
	table map[string]string
}

// LookupStage left-joins a static CSV onto the EventTable by equality on
// KeyField, adding one new column per requested output. Rows whose key has no
// matching CSV row get nil for every output column.
type LookupStage struct {
	KeyField string
	Outputs  []lookupOutput
}

func (s *LookupStage) CollectFields(out map[string]bool) { out[s.KeyField] = true }

func (s *LookupStage) ProducedFields() []string {
	out := make([]string, len(s.Outputs))
	for i, o := range s.Outputs {
		out[i] = o.alias
	}
	return out
}

func (s *LookupStage) Exec(t *EventTable) (*EventTable, error) {
	keyCol, ok := t.Columns[s.KeyField]
	if !ok {
		return nil, fmt.Errorf("unknown field: %s", s.KeyField)
	}
	for _, o := range s.Outputs {
		col := make([]any, len(t.Timestamps))
		for i := range t.Timestamps {
			if keyCol[i] == nil {
				continue
			}
			if v, ok := o.table[fmt.Sprint(keyCol[i])]; ok {
				col[i] = v
			}
		}
		t.Columns[o.alias] = col
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

// ProducedFields reports the columns stats emits (aggregate aliases plus the
// group-by keys) so a downstream stage referencing them isn't mistaken for a
// session field.
func (s *StatsStage) ProducedFields() []string {
	out := make([]string, 0, len(s.Aggs)+len(s.GroupBy))
	for _, a := range s.Aggs {
		out = append(out, a.Alias)
	}
	out = append(out, s.GroupBy...)
	return out
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
		aggVals, err := evalAggs(t, g.rowIdx, s.Aggs)
		if err != nil {
			return nil, err
		}
		for ai, a := range s.Aggs {
			out.Columns[a.Alias] = append(out.Columns[a.Alias], aggVals[ai])
		}
	}
	return out, nil
}

// --- timechart ---

// TimechartStage buckets rows into fixed-width time spans (and, optionally,
// by a group field's value) and applies the same aggregate functions as
// `stats` to each bucket. Output rows are sorted by bucket start, then group.
type TimechartStage struct {
	SpanUs  int64
	Aggs    []AggCall
	GroupBy string // "" if no `by` clause
}

func (s *TimechartStage) CollectFields(out map[string]bool) {
	for _, a := range s.Aggs {
		if a.Field != "" {
			out[a.Field] = true
		}
	}
	if s.GroupBy != "" {
		out[s.GroupBy] = true
	}
}

func (s *TimechartStage) ProducedFields() []string {
	out := []string{"Timestamp"}
	if s.GroupBy != "" {
		out = append(out, s.GroupBy)
	}
	for _, a := range s.Aggs {
		out = append(out, a.Alias)
	}
	return out
}

func (s *TimechartStage) Exec(t *EventTable) (*EventTable, error) {
	if s.SpanUs <= 0 {
		return nil, fmt.Errorf("timechart span must be positive")
	}
	idx := visibleIndices(t)

	type bucketKey struct {
		bucket int64
		group  any
	}
	type bucketData struct {
		key    bucketKey
		rowIdx []int
	}
	buckets := make(map[bucketKey]*bucketData)
	var order []bucketKey
	for _, i := range idx {
		bucket := (t.Timestamps[i] / s.SpanUs) * s.SpanUs
		var group any
		if s.GroupBy != "" {
			group = t.Columns[s.GroupBy][i]
		}
		k := bucketKey{bucket: bucket, group: group}
		bd, ok := buckets[k]
		if !ok {
			bd = &bucketData{key: k}
			buckets[k] = bd
			order = append(order, k)
		}
		bd.rowIdx = append(bd.rowIdx, i)
	}
	sort.Slice(order, func(a, b int) bool {
		if order[a].bucket != order[b].bucket {
			return order[a].bucket < order[b].bucket
		}
		return fmt.Sprint(order[a].group) < fmt.Sprint(order[b].group)
	})

	out := &EventTable{Columns: make(map[string][]any, 1+len(s.Aggs))}
	if s.GroupBy != "" {
		out.Columns[s.GroupBy] = nil
	}
	for _, a := range s.Aggs {
		out.Columns[a.Alias] = nil
	}
	for _, k := range order {
		bd := buckets[k]
		out.Timestamps = append(out.Timestamps, k.bucket)
		out.Mask = append(out.Mask, true)
		if s.GroupBy != "" {
			out.Columns[s.GroupBy] = append(out.Columns[s.GroupBy], k.group)
		}
		aggVals, err := evalAggs(t, bd.rowIdx, s.Aggs)
		if err != nil {
			return nil, err
		}
		for ai, a := range s.Aggs {
			out.Columns[a.Alias] = append(out.Columns[a.Alias], aggVals[ai])
		}
	}
	return out, nil
}

// --- transaction ---

// TransactionStage is a ClaudeScope extension (not SPL) that generalizes
// `ranges`/FindRuns to an arbitrary start/end predicate pair. Rows from a
// `start` match up to (and including) the next `end` match are grouped into
// one transaction and stamped with a "transactionID" column; rows outside any
// transaction are masked out. A transaction still open at the end of the
// visible rows keeps its ID (matches FindRuns's "close at log end" behavior).
type TransactionStage struct {
	Start Expr
	End   Expr
}

func (s *TransactionStage) CollectFields(out map[string]bool) {
	s.Start.CollectFields(out)
	s.End.CollectFields(out)
}

func (s *TransactionStage) ProducedFields() []string { return []string{"transactionID"} }

func (s *TransactionStage) Exec(t *EventTable) (*EventTable, error) {
	idx := visibleIndices(t)
	tid := make([]any, len(t.Timestamps))
	inTxn := false
	txnNum := 0
	for _, i := range idx {
		if !inTxn {
			started, err := evalBool(s.Start, t.RowAt(i), "transaction start")
			if err != nil {
				return nil, err
			}
			if !started {
				t.Mask[i] = false
				continue
			}
			inTxn = true
			txnNum++
		}
		tid[i] = fmt.Sprintf("%d", txnNum)
		ended, err := evalBool(s.End, t.RowAt(i), "transaction end")
		if err != nil {
			return nil, err
		}
		if ended {
			inTxn = false
		}
	}
	t.Columns["transactionID"] = tid
	return t, nil
}

func evalBool(e Expr, row Row, what string) (bool, error) {
	v, err := e.Eval(row)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("%s expression did not evaluate to a boolean", what)
	}
	return b, nil
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
