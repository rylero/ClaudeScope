package query

import (
	"fmt"
	"regexp"
	"strings"
)

// Pipeline is a parsed, ready-to-run query.
type Pipeline struct {
	Stages []Stage
}

// Parse compiles a pipe query string into a Pipeline, e.g.
// `where CurrentA > 40 and CurrentB > 40 | stats avg(BatteryVoltage) by Subsystem`.
// Any “ `macro` “ backtick-references are expanded first (see LoadMacros).
func Parse(input string) (*Pipeline, error) {
	macros, err := LoadMacros()
	if err != nil {
		return nil, err
	}
	input, err = expandMacros(input, macros)
	if err != nil {
		return nil, err
	}
	toks, err := lex(input)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	var stages []Stage
	for {
		st, err := p.parseStage()
		if err != nil {
			return nil, err
		}
		stages = append(stages, st)
		if p.cur().kind == tPipe {
			p.pos++
			continue
		}
		break
	}
	if p.cur().kind != tEOF {
		return nil, fmt.Errorf("unexpected token %q after query", p.cur().text)
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("empty query")
	}
	return &Pipeline{Stages: stages}, nil
}

type parser struct {
	toks []token
	pos  int
}

func (p *parser) cur() token { return p.toks[p.pos] }

// canonField maps SPL-idiomatic field aliases onto ClaudeScope's canonical
// names. `_time` is Splunk's timestamp field; we expose it as an alias for the
// EventTable's `Timestamp` pseudo-column.
func canonField(name string) string {
	if name == "_time" {
		return "Timestamp"
	}
	return name
}

func (p *parser) parseStage() (Stage, error) {
	t := p.cur()
	if t.kind != tIdent {
		return nil, fmt.Errorf("expected stage keyword, got %q", t.text)
	}
	switch strings.ToLower(t.text) {
	case "where", "search": // `search` is the SPL alias for a leading filter
		p.pos++
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &WhereStage{Expr: expr}, nil
	case "stats":
		return p.parseStats()
	case "table", "fields": // `fields` is the SPL alias for column selection
		return p.parseTable()
	case "sort":
		return p.parseSort()
	case "head":
		return p.parseHeadTail(false)
	case "tail":
		return p.parseHeadTail(true)
	case "eval":
		return p.parseEval()
	case "rex":
		return p.parseRex()
	case "timechart":
		return p.parseTimechart()
	case "lookup":
		return p.parseLookup()
	case "ranges":
		p.pos++
		return &RangesStage{}, nil
	case "transaction":
		return p.parseTransaction()
	default:
		return nil, fmt.Errorf(
			"unsupported command %q — this is a subset of Splunk SPL. Supported: where (or search), eval, rex, stats, timechart, lookup, table (or fields), sort, head, tail, and the ClaudeScope extensions ranges, transaction",
			t.text)
	}
}

// Expression grammar (precedence low → high):
//   or    := and ('or' and)*
//   and   := cmp ('and' cmp)*
//   cmp   := ('NOT'|'!') cmp | add (OP add)?      // comparison is optional so a
//                                                 // bare arithmetic value is valid
//   add   := mul (('+'|'-') mul)*
//   mul   := unary (('*'|'/') unary)*
//   unary := '-' unary | primary
//   primary := NUMBER | STRING | 'true' | 'false'
//            | IDENT '(' args ')'   (function call)
//            | IDENT                (field ref)
//            | '(' or ')'

func (p *parser) parseExpr() (Expr, error) { return p.parseOr() }

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "or") {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "and") {
		p.pos++
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseCmp() (Expr, error) {
	// SPL-style negation: `NOT expr` or `!expr`.
	if p.cur().kind == tBang || (p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "not")) {
		p.pos++
		inner, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		return &NotExpr{Inner: inner}, nil
	}
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	// The comparison is optional: `eval d = a - b` has no comparison operator.
	if op, ok := cmpOp(p.cur().kind); ok {
		p.pos++
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: op, Left: left, Right: right}, nil
	}
	return left, nil
}

func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tPlus || p.cur().kind == tMinus {
		op := "+"
		if p.cur().kind == tMinus {
			op = "-"
		}
		p.pos++
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseMul() (Expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.cur().kind == tStar || p.cur().kind == tSlash {
		op := "*"
		if p.cur().kind == tSlash {
			op = "/"
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.cur().kind == tMinus {
		p.pos++
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		// Fold a leading minus on a literal; otherwise 0 - inner.
		if n, ok := inner.(*NumberLit); ok {
			return &NumberLit{Value: -n.Value}, nil
		}
		return &BinaryExpr{Op: "-", Left: &NumberLit{Value: 0}, Right: inner}, nil
	}
	return p.parsePrimary()
}

func cmpOp(k tokKind) (string, bool) {
	switch k {
	case tGT:
		return ">", true
	case tLT:
		return "<", true
	case tGE:
		return ">=", true
	case tLE:
		return "<=", true
	case tEQ:
		return "==", true
	case tNE:
		return "!=", true
	}
	return "", false
}

func (p *parser) parsePrimary() (Expr, error) {
	t := p.cur()
	switch t.kind {
	case tLParen:
		p.pos++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.cur().kind != tRParen {
			return nil, fmt.Errorf("expected ')'")
		}
		p.pos++
		return e, nil
	case tNumber:
		p.pos++
		return &NumberLit{Value: t.num}, nil
	case tString:
		p.pos++
		return &StringLit{Value: t.text}, nil
	case tIdent:
		p.pos++
		// Function call: IDENT immediately followed by '('.
		if p.cur().kind == tLParen {
			return p.parseFuncArgs(strings.ToLower(t.text))
		}
		if strings.EqualFold(t.text, "true") {
			return &BoolLit{Value: true}, nil
		}
		if strings.EqualFold(t.text, "false") {
			return &BoolLit{Value: false}, nil
		}
		return &FieldRef{Name: canonField(t.text)}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// parseFuncArgs parses `( expr (',' expr)* )` after a function name.
func (p *parser) parseFuncArgs(name string) (Expr, error) {
	p.pos++ // consume '('
	var args []Expr
	if p.cur().kind != tRParen {
		for {
			a, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if p.cur().kind == tComma {
				p.pos++
				continue
			}
			break
		}
	}
	if p.cur().kind != tRParen {
		return nil, fmt.Errorf("expected ')' to close %s(", name)
	}
	p.pos++
	return &FuncExpr{Name: name, Args: args}, nil
}

// --- stats stage ---

var validAggFns = map[string]bool{
	"avg": true, "min": true, "max": true, "sum": true, "count": true,
	"median": true, "stdev": true, "p50": true, "p90": true, "p99": true,
}

// parseAggCallList parses `agg_call (',' agg_call)*` (comma optional; SPL also
// allows plain spaces), stopping as soon as the next token isn't a known
// aggregate function name. Shared by `stats` and `timechart`.
func (p *parser) parseAggCallList() ([]AggCall, error) {
	var aggs []AggCall
	for {
		agg, err := p.parseAggCall()
		if err != nil {
			return nil, err
		}
		aggs = append(aggs, agg)
		if p.cur().kind == tComma {
			p.pos++
		}
		if p.cur().kind == tIdent && validAggFns[strings.ToLower(p.cur().text)] {
			continue
		}
		break
	}
	return aggs, nil
}

func (p *parser) parseStats() (Stage, error) {
	p.pos++ // consume 'stats'
	aggs, err := p.parseAggCallList()
	if err != nil {
		return nil, err
	}
	var groupBy []string
	if p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "by") {
		p.pos++
		for {
			f := p.cur()
			if f.kind != tIdent {
				return nil, fmt.Errorf("expected field name after 'by'")
			}
			groupBy = append(groupBy, f.text)
			p.pos++
			if p.cur().kind == tComma { // comma optional
				p.pos++
			}
			if p.cur().kind != tIdent {
				break
			}
		}
	}
	return &StatsStage{Aggs: aggs, GroupBy: groupBy}, nil
}

func (p *parser) parseAggCall() (AggCall, error) {
	t := p.cur()
	if t.kind != tIdent || !validAggFns[strings.ToLower(t.text)] {
		return AggCall{}, fmt.Errorf("expected aggregate function, got %q", t.text)
	}
	fn := strings.ToLower(t.text)
	p.pos++

	var field string
	if p.cur().kind == tLParen {
		p.pos++
		f := p.cur()
		if f.kind != tIdent {
			return AggCall{}, fmt.Errorf("expected field name inside %s(...)", fn)
		}
		field = f.text
		p.pos++
		if p.cur().kind != tRParen {
			return AggCall{}, fmt.Errorf("expected ')'")
		}
		p.pos++
	} else if fn != "count" {
		return AggCall{}, fmt.Errorf("%s requires a field: %s(field)", fn, fn)
	}

	alias := fn
	if field != "" {
		alias = fmt.Sprintf("%s(%s)", fn, field)
	}
	if p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "as") {
		p.pos++
		a := p.cur()
		if a.kind != tIdent {
			return AggCall{}, fmt.Errorf("expected alias name after 'as'")
		}
		alias = a.text
		p.pos++
	}
	return AggCall{Fn: fn, Field: field, Alias: alias}, nil
}

// --- table / sort / head / tail ---

func (p *parser) parseTable() (Stage, error) {
	p.pos++
	var fields []string
	for {
		f := p.cur()
		if f.kind != tIdent {
			return nil, fmt.Errorf("expected field name")
		}
		fields = append(fields, canonField(f.text))
		p.pos++
		if p.cur().kind == tComma { // comma is optional; SPL also allows spaces
			p.pos++
		}
		if p.cur().kind != tIdent {
			break
		}
	}
	return &TableStage{Fields: fields}, nil
}

func (p *parser) parseSort() (Stage, error) {
	p.pos++
	desc := false
	if p.cur().kind == tMinus {
		desc = true
		p.pos++
	}
	f := p.cur()
	if f.kind != tIdent {
		return nil, fmt.Errorf("expected field name after 'sort'")
	}
	p.pos++
	return &SortStage{Field: canonField(f.text), Desc: desc}, nil
}

// --- eval / rex ---

func (p *parser) parseEval() (Stage, error) {
	p.pos++ // consume 'eval'
	name := p.cur()
	if name.kind != tIdent {
		return nil, fmt.Errorf("expected a column name after 'eval'")
	}
	p.pos++
	if p.cur().kind != tEQ {
		return nil, fmt.Errorf("expected '=' in eval assignment: eval <name> = <expr>")
	}
	p.pos++
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &EvalStage{Target: canonField(name.text), Expr: expr}, nil
}

func (p *parser) parseRex() (Stage, error) {
	p.pos++ // consume 'rex'
	// SPL syntax: rex field=<field> "<regex with named groups>"
	kw := p.cur()
	if kw.kind != tIdent || !strings.EqualFold(kw.text, "field") {
		return nil, fmt.Errorf(`rex requires: rex field=<field> "<regex>"`)
	}
	p.pos++
	if p.cur().kind != tEQ {
		return nil, fmt.Errorf("expected '=' after 'field' in rex")
	}
	p.pos++
	fld := p.cur()
	if fld.kind != tIdent {
		return nil, fmt.Errorf("expected a field name after 'field=' in rex")
	}
	p.pos++
	pat := p.cur()
	if pat.kind != tString {
		return nil, fmt.Errorf("rex requires a quoted regular expression")
	}
	p.pos++
	// Accept Splunk/PCRE named-group syntax `(?<name>...)` by translating it to
	// Go's `(?P<name>...)`.
	goPat := strings.ReplaceAll(pat.text, "(?<", "(?P<")
	re, err := regexp.Compile(goPat)
	if err != nil {
		return nil, fmt.Errorf("invalid rex regular expression: %w", err)
	}
	var groups []string
	for _, name := range re.SubexpNames() {
		if name != "" {
			groups = append(groups, name)
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("rex regular expression must define at least one named group, e.g. (?<channel>\\d+)")
	}
	return &RexStage{Field: canonField(fld.text), Re: re, Groups: groups}, nil
}

// --- lookup ---

// parseLookup is a ClaudeScope extension, simplified from real SPL's lookup
// (which supports lookup tables registered by name and more elaborate
// AS/OUTPUT renaming): `lookup "<path>.csv" <field> output <col> [as <alias>]
// (',' <col> [as <alias>])*`. <field> must also be the CSV's key column name.
func (p *parser) parseLookup() (Stage, error) {
	p.pos++ // consume 'lookup'
	pathTok := p.cur()
	if pathTok.kind != tString {
		return nil, fmt.Errorf(`lookup requires a quoted CSV path: lookup "<path>" <field> output <column>...`)
	}
	p.pos++
	keyTok := p.cur()
	if keyTok.kind != tIdent {
		return nil, fmt.Errorf("expected a field name after the lookup path")
	}
	keyField := canonField(keyTok.text)
	p.pos++
	if !(p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "output")) {
		return nil, fmt.Errorf("lookup requires 'output <column>...' after the key field")
	}
	p.pos++

	type outSpec struct{ col, alias string }
	var specs []outSpec
	for {
		c := p.cur()
		if c.kind != tIdent {
			return nil, fmt.Errorf("expected a CSV column name after 'output'")
		}
		p.pos++
		alias := c.text
		if p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "as") {
			p.pos++
			a := p.cur()
			if a.kind != tIdent {
				return nil, fmt.Errorf("expected alias name after 'as'")
			}
			alias = a.text
			p.pos++
		}
		specs = append(specs, outSpec{col: c.text, alias: alias})
		if p.cur().kind != tComma {
			break
		}
		p.pos++
	}

	cols := make([]string, len(specs))
	for i, s := range specs {
		cols[i] = s.col
	}
	tables, err := loadLookupTables(pathTok.text, keyField, cols)
	if err != nil {
		return nil, err
	}
	outputs := make([]lookupOutput, len(specs))
	for i, s := range specs {
		outputs[i] = lookupOutput{alias: s.alias, table: tables[s.col]}
	}
	return &LookupStage{KeyField: keyField, Outputs: outputs}, nil
}

// --- timechart / transaction ---

// parseDuration parses a NUMBER followed by an optional unit ident
// (us, ms, s, m, h, d — default s), returning microseconds. Adjacent tokens
// like "1s" or "500ms" lex as NUMBER then IDENT with no gap to bridge.
func (p *parser) parseDuration() (int64, error) {
	if p.cur().kind != tNumber {
		return 0, fmt.Errorf("expected a duration, e.g. span=1s")
	}
	n := p.cur().num
	p.pos++
	unit := "s"
	if p.cur().kind == tIdent {
		unit = strings.ToLower(p.cur().text)
		p.pos++
	}
	var mult float64
	switch unit {
	case "us", "µs":
		mult = 1
	case "ms":
		mult = 1_000
	case "s":
		mult = 1_000_000
	case "m":
		mult = 60_000_000
	case "h":
		mult = 3_600_000_000
	case "d":
		mult = 86_400_000_000
	default:
		return 0, fmt.Errorf("unknown duration unit %q (expected us, ms, s, m, h, or d)", unit)
	}
	return int64(n * mult), nil
}

func (p *parser) parseTimechart() (Stage, error) {
	p.pos++ // consume 'timechart'
	kw := p.cur()
	if kw.kind != tIdent || !strings.EqualFold(kw.text, "span") {
		return nil, fmt.Errorf("timechart requires span=<duration>, e.g. timechart span=1s avg(field)")
	}
	p.pos++
	if p.cur().kind != tEQ {
		return nil, fmt.Errorf("expected '=' after 'span'")
	}
	p.pos++
	spanUs, err := p.parseDuration()
	if err != nil {
		return nil, err
	}
	aggs, err := p.parseAggCallList()
	if err != nil {
		return nil, err
	}
	var groupBy string
	if p.cur().kind == tIdent && strings.EqualFold(p.cur().text, "by") {
		p.pos++
		f := p.cur()
		if f.kind != tIdent {
			return nil, fmt.Errorf("expected field name after 'by'")
		}
		groupBy = canonField(f.text)
		p.pos++
	}
	return &TimechartStage{SpanUs: spanUs, Aggs: aggs, GroupBy: groupBy}, nil
}

// parseTransaction is a ClaudeScope extension (SPL's `transaction` has quite
// different, field-correlation-based semantics): `transaction start=<expr>
// end=<expr>` groups rows into episodes bounded by the two predicates.
func (p *parser) parseTransaction() (Stage, error) {
	p.pos++ // consume 'transaction'
	var startExpr, endExpr Expr
	for i := 0; i < 2; i++ {
		kw := p.cur()
		key := strings.ToLower(kw.text)
		if kw.kind != tIdent || (key != "start" && key != "end") {
			return nil, fmt.Errorf("transaction requires start=<expr> and end=<expr>, got %q", kw.text)
		}
		p.pos++
		if p.cur().kind != tEQ {
			return nil, fmt.Errorf("expected '=' after %q in transaction", key)
		}
		p.pos++
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if key == "start" {
			if startExpr != nil {
				return nil, fmt.Errorf("duplicate 'start' in transaction")
			}
			startExpr = e
		} else {
			if endExpr != nil {
				return nil, fmt.Errorf("duplicate 'end' in transaction")
			}
			endExpr = e
		}
	}
	return &TransactionStage{Start: startExpr, End: endExpr}, nil
}

func (p *parser) parseHeadTail(tail bool) (Stage, error) {
	p.pos++
	n := p.cur()
	if n.kind != tNumber {
		return nil, fmt.Errorf("expected a number")
	}
	p.pos++
	if tail {
		return &TailStage{N: int(n.num)}, nil
	}
	return &HeadStage{N: int(n.num)}, nil
}
