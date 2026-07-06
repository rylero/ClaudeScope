package query

import (
	"fmt"
	"sort"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// SessionResult is one session's outcome from RunAcross: either Result (the
// same shape Pipeline.Run returns for a single session — []map[string]any or
// []session.TimeRange) or Error, never both.
type SessionResult struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label,omitempty"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RunAcross parses queryStr once and runs it against every session, so a
// single typo doesn't produce N confusingly-identical parse errors. Each
// session's own execution error (e.g. a field missing from that particular
// log) is captured in its SessionResult rather than aborting the batch — one
// bad log in a folder of match logs shouldn't blank out the rest.
func RunAcross(sessions map[string]session.DataSession, queryStr string, start, end int64) ([]SessionResult, error) {
	pipeline, err := Parse(queryStr)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]SessionResult, 0, len(ids))
	for _, id := range ids {
		res, err := pipeline.Run(sessions[id], start, end)
		sr := SessionResult{SessionID: id}
		if err != nil {
			sr.Error = err.Error()
		} else {
			sr.Result = res
		}
		out = append(out, sr)
	}
	return out, nil
}

// UnionResults flattens a comparison-shaped RunAcross result into one table:
// every row (or every ranges interval) is tagged with its source session_id.
// Results that errored are returned separately in errored rather than
// silently dropped. Union only makes sense when every successful result
// shares the same shape (all table rows or all ranges) — a mixed batch
// returns an error naming the offending session.
func UnionResults(results []SessionResult) (rows []map[string]any, errored []SessionResult, err error) {
	for _, r := range results {
		if r.Error != "" {
			errored = append(errored, r)
			continue
		}
		switch v := r.Result.(type) {
		case []map[string]any:
			for _, row := range v {
				tagged := make(map[string]any, len(row)+1)
				for k, val := range row {
					tagged[k] = val
				}
				tagged["session_id"] = r.SessionID
				rows = append(rows, tagged)
			}
		case []session.TimeRange:
			for _, tr := range v {
				rows = append(rows, map[string]any{
					"session_id": r.SessionID,
					"start":      tr.Start,
					"end":        tr.End,
				})
			}
		default:
			return nil, nil, fmt.Errorf("session %s: unrecognized result shape for union", r.SessionID)
		}
	}
	return rows, errored, nil
}
