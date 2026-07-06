package query

import (
	"fmt"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

// Row is a single forward-filled event: field name -> current value.
type Row map[string]any

// Expr is a scalar expression evaluated per-row (field refs, literals, comparisons).
type Expr interface {
	Eval(row Row) (any, error)
	CollectFields(out map[string]bool)
}

type FieldRef struct{ Name string }

func (f *FieldRef) Eval(row Row) (any, error)         { return row[f.Name], nil }
func (f *FieldRef) CollectFields(out map[string]bool) { out[f.Name] = true }

type NumberLit struct{ Value float64 }

func (l *NumberLit) Eval(Row) (any, error)             { return l.Value, nil }
func (l *NumberLit) CollectFields(out map[string]bool) {}

type StringLit struct{ Value string }

func (l *StringLit) Eval(Row) (any, error)             { return l.Value, nil }
func (l *StringLit) CollectFields(out map[string]bool) {}

type BoolLit struct{ Value bool }

func (l *BoolLit) Eval(Row) (any, error)             { return l.Value, nil }
func (l *BoolLit) CollectFields(out map[string]bool) {}

// NotExpr is SPL-style boolean negation (`NOT expr` / `!expr`).
type NotExpr struct{ Inner Expr }

func (e *NotExpr) CollectFields(out map[string]bool) { e.Inner.CollectFields(out) }

func (e *NotExpr) Eval(row Row) (any, error) {
	v, err := e.Inner.Eval(row)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("NOT operand did not evaluate to a boolean")
	}
	return !b, nil
}

// BinaryExpr covers both boolean combinators (and/or) and comparisons
// (> < >= <= == !=).
type BinaryExpr struct {
	Op          string
	Left, Right Expr
}

func (b *BinaryExpr) CollectFields(out map[string]bool) {
	b.Left.CollectFields(out)
	b.Right.CollectFields(out)
}

func (b *BinaryExpr) Eval(row Row) (any, error) {
	switch b.Op {
	case "and", "or":
		lv, err := b.Left.Eval(row)
		if err != nil {
			return nil, err
		}
		lb, ok := lv.(bool)
		if !ok {
			return nil, fmt.Errorf("left side of %q did not evaluate to a boolean", b.Op)
		}
		if b.Op == "and" && !lb {
			return false, nil
		}
		if b.Op == "or" && lb {
			return true, nil
		}
		rv, err := b.Right.Eval(row)
		if err != nil {
			return nil, err
		}
		rb, ok := rv.(bool)
		if !ok {
			return nil, fmt.Errorf("right side of %q did not evaluate to a boolean", b.Op)
		}
		return rb, nil
	default:
		lv, err := b.Left.Eval(row)
		if err != nil {
			return nil, err
		}
		rv, err := b.Right.Eval(row)
		if err != nil {
			return nil, err
		}
		return compareValues(b.Op, lv, rv)
	}
}

func compareValues(op string, l, r any) (bool, error) {
	if lf, err1 := session.ToFloat64(l); err1 == nil {
		if rf, err2 := session.ToFloat64(r); err2 == nil {
			switch op {
			case ">":
				return lf > rf, nil
			case "<":
				return lf < rf, nil
			case ">=":
				return lf >= rf, nil
			case "<=":
				return lf <= rf, nil
			case "==":
				return lf == rf, nil
			case "!=":
				return lf != rf, nil
			}
		}
	}
	switch op {
	case "==":
		return fmt.Sprint(l) == fmt.Sprint(r), nil
	case "!=":
		return fmt.Sprint(l) != fmt.Sprint(r), nil
	default:
		return false, fmt.Errorf("operator %q not supported between %T and %T", op, l, r)
	}
}
