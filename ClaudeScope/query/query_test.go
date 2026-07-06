package query_test

import (
	"os"
	"path/filepath"
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
	_, err := query.Parse("where CurrentA > 40 | dedup CurrentA")
	if err == nil {
		t.Fatal("expected error for unsupported 'dedup' command")
	}
	if !strings.Contains(err.Error(), "subset of Splunk SPL") {
		t.Errorf("error should point the user at the supported SPL subset, got: %v", err)
	}
}

func TestEvalArithmetic(t *testing.T) {
	sess := buildTestLog()
	// At t=2000: CurrentA=45, CurrentB=55 -> delta = -10, abs = 10.
	res, err := query.Run(sess, "eval delta = abs(CurrentA - CurrentB) | where _time == 2000 | table delta", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0]["delta"].(float64) != 10 {
		t.Errorf("expected delta=10, got %v", rows[0]["delta"])
	}
}

func TestEvalThenWhereUsesComputedColumn(t *testing.T) {
	sess := buildTestLog()
	// sum = CurrentA + CurrentB; keep rows where sum > 90. Only t=2000 (45+55=100).
	res, err := query.Run(sess, "eval sum = CurrentA + CurrentB | where sum > 90 | table _time sum", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 || rows[0]["Timestamp"] != int64(2000) || rows[0]["sum"].(float64) != 100 {
		t.Errorf("expected single row t=2000 sum=100, got %+v", rows)
	}
}

func TestEvalDivisionAndPathsCoexist(t *testing.T) {
	sess := buildTestLog()
	// Division operator must not collide with FRC field-path '/'. ratio at
	// t=1000: 50/20 = 2.5.
	res, err := query.Run(sess, "eval ratio = CurrentA / CurrentB | where _time == 1000 | table ratio", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 || rows[0]["ratio"].(float64) != 2.5 {
		t.Errorf("expected ratio=2.5, got %+v", rows)
	}
}

func TestRexExtractsNamedGroup(t *testing.T) {
	sess := buildTestLog()
	// SPL-style (?<ch>...) named group, forward-filled: ch=3 until t=3000, then 7.
	res, err := query.Run(sess, `rex field=Message "channel (?<ch>\d+)" | table _time ch`, 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) == 0 {
		t.Fatal("expected rows")
	}
	first, last := rows[0], rows[len(rows)-1]
	if first["ch"] != "3" {
		t.Errorf("expected first ch=3, got %v", first["ch"])
	}
	if last["ch"] != "7" {
		t.Errorf("expected last ch=7, got %v", last["ch"])
	}
}

func TestSlashPathFieldWithDivision(t *testing.T) {
	sess := buildTestLog()
	// `/PDH/Voltage` is a slash-path field; `/ 2` in the same query is division.
	// At t=0: /PDH/Voltage=12 -> half=6.
	res, err := query.Run(sess, "eval half = /PDH/Voltage / 2 | where _time == 0 | table half", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 || rows[0]["half"].(float64) != 6 {
		t.Errorf("expected half=6, got %+v", rows)
	}
}

func TestSlashPathFieldInWhere(t *testing.T) {
	sess := buildTestLog()
	res, err := query.Run(sess, "where /PDH/Voltage < 7 | ranges", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := res.([]session.TimeRange)
	want, err := sess.FindThresholdRanges("/PDH/Voltage", -1e18, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || (len(want) > 0 && got[0] != want[0]) {
		t.Errorf("slash-path where|ranges got %+v, want %+v", got, want)
	}
}

func TestTimechartBucketsBySpan(t *testing.T) {
	sess := buildTestLog()
	// span=1ms=1000us matches the fixture's own point spacing (0,1000,2000,3000),
	// so each CurrentA sample lands in its own bucket.
	res, err := query.Run(sess, "timechart span=1ms avg(CurrentA)", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 4 {
		t.Fatalf("expected 4 buckets, got %d: %+v", len(rows), rows)
	}
	wantBuckets := []int64{0, 1000, 2000, 3000}
	wantAvg := []float64{10, 50, 45, 5}
	for i, row := range rows {
		if row["Timestamp"] != wantBuckets[i] {
			t.Errorf("row %d: bucket = %v, want %v", i, row["Timestamp"], wantBuckets[i])
		}
		if row["avg(CurrentA)"].(float64) != wantAvg[i] {
			t.Errorf("row %d: avg(CurrentA) = %v, want %v", i, row["avg(CurrentA)"], wantAvg[i])
		}
	}
}

func TestTimechartGroupBySplitsSameBucket(t *testing.T) {
	sess := buildTestLog()
	// Enabled flips false at t=2500, which falls in the same 1000us bucket
	// (2000) as CurrentA's t=2000 sample -> two rows for bucket 2000, one per
	// Enabled value.
	res, err := query.Run(sess, "timechart span=1ms avg(CurrentA) by Enabled", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 5 {
		t.Fatalf("expected 5 (bucket,group) rows, got %d: %+v", len(rows), rows)
	}
	var bucket2000Groups []any
	for _, row := range rows {
		if row["Timestamp"] == int64(2000) {
			bucket2000Groups = append(bucket2000Groups, row["Enabled"])
		}
	}
	if len(bucket2000Groups) != 2 {
		t.Errorf("expected bucket 2000 split into 2 groups, got %+v", bucket2000Groups)
	}
}

func TestTransactionGroupsAndMasksOutsideRows(t *testing.T) {
	sess := buildTestLog()
	// Enabled is true from t=0, goes false at t=2500. Rows [0,1000,2000,2500]
	// form one transaction; t=3000 (Enabled still false, no new start) is
	// masked out.
	res, err := query.Run(sess, "transaction start=(Enabled == true) end=(Enabled == false) | table _time transactionID CurrentA", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows in the transaction, got %d: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row["transactionID"] != "1" {
			t.Errorf("expected transactionID=1 for row %+v", row)
		}
	}
	last := rows[len(rows)-1]["Timestamp"]
	if last != int64(2500) {
		t.Errorf("expected transaction to close at t=2500, last row was t=%v", last)
	}
}

func writeCSV(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lookup.csv")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookupJoinsOnKeyField(t *testing.T) {
	sess := buildTestLog()
	// CurrentA values are 10,50,45,5; map each to a subsystem name.
	csvPath := writeCSV(t, "CurrentA,Subsystem\n10,Drive\n50,Shooter\n45,Intake\n5,Climber\n")
	q := `lookup "` + csvPath + `" CurrentA output Subsystem`
	res, err := query.Run(sess, q+" | table _time CurrentA Subsystem", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	want := map[int64]string{0: "Drive", 1000: "Shooter", 2000: "Intake", 3000: "Climber"}
	if len(rows) != len(want) {
		t.Fatalf("expected %d rows, got %d: %+v", len(want), len(rows), rows)
	}
	for _, row := range rows {
		ts := row["Timestamp"].(int64)
		if row["Subsystem"] != want[ts] {
			t.Errorf("t=%d: Subsystem = %v, want %v", ts, row["Subsystem"], want[ts])
		}
	}
}

func TestLookupUnmatchedKeyIsNil(t *testing.T) {
	sess := buildTestLog()
	csvPath := writeCSV(t, "CurrentA,Subsystem\n999,Nowhere\n")
	res, err := query.Run(sess, `lookup "`+csvPath+`" CurrentA output Subsystem | table _time Subsystem`, 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	for _, row := range rows {
		if row["Subsystem"] != nil {
			t.Errorf("expected nil Subsystem for unmatched key, got %+v", row)
		}
	}
}

func TestLookupAliasAndMultipleOutputs(t *testing.T) {
	sess := buildTestLog()
	csvPath := writeCSV(t, "CurrentA,Subsystem,Amps\n10,Drive,LOW\n50,Shooter,HIGH\n45,Intake,HIGH\n5,Climber,LOW\n")
	res, err := query.Run(sess,
		`lookup "`+csvPath+`" CurrentA output Subsystem as sys, Amps as level | where _time == 1000 | table sys level`,
		0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 || rows[0]["sys"] != "Shooter" || rows[0]["level"] != "HIGH" {
		t.Errorf("got %+v, want sys=Shooter level=HIGH", rows)
	}
}

func TestLookupMissingFileErrors(t *testing.T) {
	sess := buildTestLog()
	_, err := query.Run(sess, `lookup "/nonexistent/path.csv" CurrentA output Subsystem`, 0, 0)
	if err == nil {
		t.Fatal("expected error for missing lookup file")
	}
}

func TestMacroExpansion(t *testing.T) {
	dir := t.TempDir()
	macrosPath := filepath.Join(dir, "macros.json")
	if err := os.WriteFile(macrosPath, []byte(`{"lowbattery": "where Enabled == false | ranges"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	query.SetMacrosPath(macrosPath)
	defer query.SetMacrosPath("") // reset so later tests don't hit a stale path

	sess := buildTestLog()
	got, err := query.Run(sess, "`lowbattery`", 0, 0)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want, err := query.Run(sess, "where Enabled == false | ranges", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	gr, wr := got.([]session.TimeRange), want.([]session.TimeRange)
	if len(gr) != len(wr) || (len(wr) > 0 && gr[0] != wr[0]) {
		t.Errorf("macro expansion: got %+v, want %+v", gr, wr)
	}
}

func TestMacroUnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	macrosPath := filepath.Join(dir, "macros.json")
	if err := os.WriteFile(macrosPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	query.SetMacrosPath(macrosPath)
	defer query.SetMacrosPath("")

	sess := buildTestLog()
	_, err := query.Run(sess, "`doesnotexist`", 0, 0)
	if err == nil {
		t.Fatal("expected error for undefined macro")
	}
}

func TestMacroCycleDetected(t *testing.T) {
	dir := t.TempDir()
	macrosPath := filepath.Join(dir, "macros.json")
	// `a` expands to `b` and vice versa: infinite loop without depth limiting.
	if err := os.WriteFile(macrosPath, []byte(`{"a": "`+"`b`"+`", "b": "`+"`a`"+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	query.SetMacrosPath(macrosPath)
	defer query.SetMacrosPath("")

	sess := buildTestLog()
	_, err := query.Run(sess, "`a`", 0, 0)
	if err == nil {
		t.Fatal("expected error for cyclic macro expansion")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"bogus stage",
		"where CurrentA >",
		"stats notarealagg(CurrentA)",
		"transaction start=(Enabled == true)",
		"timechart avg(CurrentA)",
		"timechart span=1x avg(CurrentA)",
		"lookup CurrentA output Subsystem", // missing quoted path
		`lookup "nope.csv" CurrentA`,       // missing 'output'
		"`undefinedmacro`",                 // no macros configured
	}
	for _, q := range cases {
		if _, err := query.Parse(q); err == nil {
			t.Errorf("Parse(%q): expected error, got none", q)
		}
	}
}
