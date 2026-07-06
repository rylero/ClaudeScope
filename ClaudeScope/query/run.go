package query

import (
	"fmt"
	"sort"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// Run parses and executes a pipe query string against sess over [start, end].
// The result is either []map[string]any (table/stats output) or
// []session.TimeRange (when the pipeline ends in `ranges`).
func Run(sess session.DataSession, queryStr string, start, end int64) (any, error) {
	pipeline, err := Parse(queryStr)
	if err != nil {
		return nil, err
	}
	return pipeline.Run(sess, start, end)
}

// Run executes an already-parsed pipeline.
func (p *Pipeline) Run(sess session.DataSession, start, end int64) (any, error) {
	// Walk the stages in order, tracking which columns are produced mid-pipeline
	// (by eval/rex/stats). A field is requested from the session only if it is
	// referenced before any stage produces it — computed columns must not be
	// looked up in the log.
	produced := map[string]bool{"Timestamp": true}
	needed := map[string]bool{}
	for _, st := range p.Stages {
		refs := map[string]bool{}
		st.CollectFields(refs)
		for f := range refs {
			if !produced[f] {
				needed[f] = true
			}
		}
		if fp, ok := st.(fieldProducer); ok {
			for _, f := range fp.ProducedFields() {
				produced[f] = true
			}
		}
	}
	fields := make([]string, 0, len(needed))
	for f := range needed {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	table, err := BuildEventTable(sess, fields, start, end)
	if err != nil {
		return nil, err
	}

	for i, st := range p.Stages {
		if rs, ok := st.(*RangesStage); ok {
			if i != len(p.Stages)-1 {
				return nil, fmt.Errorf("'ranges' must be the last stage in a pipeline")
			}
			return rs.Collapse(table), nil
		}
		table, err = st.Exec(table)
		if err != nil {
			return nil, err
		}
	}
	return table.Rows(), nil
}
