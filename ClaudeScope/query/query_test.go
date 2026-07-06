package query_test

import (
	"strings"
	"testing"

	"github.com/rylero/TheFRCSuite/ClaudeScope/query"
	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

func TestWhereAndTable(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "where CurrentA > 40 and CurrentB > 40 | table Timestamp,CurrentA,CurrentB", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 matching row, got %d: %+v", len(rows), rows)
	}
	if rows[0]["Timestamp"] != int64(2000) {
		t.Errorf("expected match at t=2000, got %+v", rows[0])
	}
}

func TestRangesMatchesMultiFieldPredicate(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "where CurrentA > 40 and CurrentB > 40 | ranges", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ranges := res.([]session.TimeRange)
	want := []session.TimeRange{{Start: 2000, End: 3000}}
	if len(ranges) != len(want) || ranges[0] != want[0] {
		t.Errorf("got %+v, want %+v", ranges, want)
	}
}

func TestRangesMatchesFindBoolRangesExactly(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "where Enabled == true | ranges", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.([]session.TimeRange)

	want, err := sess.FindBoolRanges("Enabled", true)
	if err != nil {
		t.Fatalf("FindBoolRanges: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("range %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStatsAvg(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "stats avg(CurrentA)", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]["avg(CurrentA)"].(float64)
	want := (10.0 + 50.0 + 45.0 + 5.0) / 4.0
	if got != want {
		t.Errorf("avg(CurrentA) = %v, want %v", got, want)
	}
}

func TestSortHead(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "table Timestamp,CurrentA | sort -CurrentA | head 1", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 || rows[0]["CurrentA"] != 50.0 {
		t.Errorf("expected top row CurrentA=50, got %+v", rows)
	}
}

func TestRangesMustBeLastStage(t *testing.T) {
	sess := buildTestLog()
	_, err := query.Run(sess, "where Enabled == true | ranges | head 1", 0, 0)
	if err == nil {
		t.Fatal("expected error when 'ranges' is not the last stage")
	}
}

// TestSPLCompat exercises the SPL-faithful surface: single `=`, `search`/`fields`
// aliases, `NOT`/`!`, space-separated field lists, and the `_time` alias. Each
// should produce the same result as its canonical form.
func TestSPLCompat(t *testing.T) {
	sess := buildTestLog()
	cases := []struct {
		name  string
		query string
	}{
		{"single-equals", "where Enabled = true | ranges"},
		{"search-alias", "search Enabled == true | ranges"},
		{"not-keyword", "where NOT Enabled == false | ranges"},
		{"bang-operator", "where !(Enabled == false) | ranges"},
	}
	want, err := sess.FindBoolRanges("Enabled", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := query.Run(sess, tc.query, 0, 0)
			if err != nil {
				t.Fatalf("Run(%q): %v", tc.query, err)
			}
			got := res.([]session.TimeRange)
			if len(got) != len(want) || (len(want) > 0 && got[0] != want[0]) {
				t.Errorf("%q: got %+v, want %+v", tc.query, got, want)
			}
		})
	}
}

func TestSPLSpaceSeparatedAndTimeAlias(t *testing.T) {
	sess := buildTestLog()
	// `fields` alias, space-separated columns, and `_time` alias all at once.
	res, err := query.Run(sess, "where CurrentA > 40 and CurrentB > 40 | fields _time CurrentA CurrentB", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0]["Timestamp"] != int64(2000) {
		t.Errorf("expected _time canonicalized to Timestamp=2000, got %+v", rows[0])
	}
	if _, ok := rows[0]["_time"]; ok {
		t.Errorf("output should canonicalize _time to Timestamp, not emit both: %+v", rows[0])
	}
}

func TestUnsupportedCommandError(t *testing.T) {
	_, err := query.Parse("where CurrentA > 40 | eval x = CurrentA - CurrentB")
	if err == nil {
		t.Fatal("expected error for unsupported 'eval' command")
	}
	if !strings.Contains(err.Error(), "subset of Splunk SPL") {
		t.Errorf("error should point the user at the supported SPL subset, got: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"bogus stage",
		"where CurrentA >",
		"stats notarealagg(CurrentA)",
	}
	for _, q := range cases {
		if _, err := query.Parse(q); err == nil {
			t.Errorf("Parse(%q): expected error, got none", q)
		}
	}
}
