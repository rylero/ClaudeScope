package query

import (
	"fmt"
	"strings"
)

// Pipeline is a parsed, ready-to-run query.
type Pipeline struct {
	Stages []Stage
}

// Parse compiles a pipe query string into a Pipeline, e.g.
// `where CurrentA > 40 and CurrentB > 40 | stats avg(BatteryVoltage) by Subsystem`.
func Parse(input string) (*Pipeline, error) {
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

func (p *parser) parseStage() (Stage, error) {
	t := p.cur()
	if t.kind != tIdent {
		return nil, fmt.Errorf("expected stage keyword, got %q", t.text)
	}
	switch strings.ToLower(t.text) {
	case "where":
		p.pos++
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &WhereStage{Expr: expr}, nil
	case "stats":
		return p.parseStats()
	case "table":
		return p.parseTable()
	case "sort":
		return p.parseSort()
	case "head":
		return p.parseHeadTail(false)
	case "tail":
		return p.parseHeadTail(true)
	case "ranges":
		p.pos++
		return &RangesStage{}, nil
	default:
		return nil, fmt.Errorf("unknown stage %q", t.text)
	}
}

// --- expr: or_expr := and_expr ('or' and_expr)*
//     and_expr := cmp ('and' cmp)*
//     cmp      := primary OP primary | '(' expr ')'

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
	if p.cur().kind == tLParen {
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
	}
	left, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	op, ok := cmpOp(p.cur().kind)
	if !ok {
		return nil, fmt.Errorf("expected comparison operator, got %q", p.cur().text)
	}
	p.pos++
	right, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return &BinaryExpr{Op: op, Left: left, Right: right}, nil
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
	case tMinus:
		p.pos++
		n := p.cur()
		if n.kind != tNumber {
			return nil, fmt.Errorf("expected number after '-'")
		}
		p.pos++
		return &NumberLit{Value: -n.num}, nil
	case tNumber:
		p.pos++
		return &NumberLit{Value: t.num}, nil
	case tString:
		p.pos++
		return &StringLit{Value: t.text}, nil
	case tIdent:
		p.pos++
		if strings.EqualFold(t.text, "true") {
			return &BoolLit{Value: true}, nil
		}
		if strings.EqualFold(t.text, "false") {
			return &BoolLit{Value: false}, nil
		}
		return &FieldRef{Name: t.text}, nil
	}
	return nil, fmt.Errorf("unexpected token %q", t.text)
}

// --- stats stage ---

var validAggFns = map[string]bool{
	"avg": true, "min": true, "max": true, "sum": true, "count": true,
	"median": true, "stdev": true, "p50": true, "p90": true, "p99": true,
}

func (p *parser) parseStats() (Stage, error) {
	p.pos++ // consume 'stats'
	var aggs []AggCall
	for {
		agg, err := p.parseAggCall()
		if err != nil {
			return nil, err
		}
		aggs = append(aggs, agg)
		if p.cur().kind == tComma {
			p.pos++
			continue
		}
		break
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
			if p.cur().kind == tComma {
				p.pos++
				continue
			}
			break
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
		fields = append(fields, f.text)
		p.pos++
		if p.cur().kind == tComma {
			p.pos++
			continue
		}
		break
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
	return &SortStage{Field: f.text, Desc: desc}, nil
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
