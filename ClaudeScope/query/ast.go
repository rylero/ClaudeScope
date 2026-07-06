package query

import (
	"fmt"
	"math"

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
	case "+", "-", "*", "/":
		lv, err := b.Left.Eval(row)
		if err != nil {
			return nil, err
		}
		rv, err := b.Right.Eval(row)
		if err != nil {
			return nil, err
		}
		lf, err := session.ToFloat64(lv)
		if err != nil {
			return nil, fmt.Errorf("left side of %q is not numeric: %w", b.Op, err)
		}
		rf, err := session.ToFloat64(rv)
		if err != nil {
			return nil, fmt.Errorf("right side of %q is not numeric: %w", b.Op, err)
		}
		switch b.Op {
		case "+":
			return lf + rf, nil
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		default: // "/"
			if rf == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return lf / rf, nil
		}
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

// FuncExpr is a scalar function call in an eval/where expression, e.g.
// abs(CurrentA - CurrentB) or round(BatteryVoltage, 1).
type FuncExpr struct {
	Name string
	Args []Expr
}

func (f *FuncExpr) CollectFields(out map[string]bool) {
	for _, a := range f.Args {
		a.CollectFields(out)
	}
}

func (f *FuncExpr) Eval(row Row) (any, error) {
	args := make([]float64, len(f.Args))
	for i, a := range f.Args {
		v, err := a.Eval(row)
		if err != nil {
			return nil, err
		}
		fv, err := session.ToFloat64(v)
		if err != nil {
			return nil, fmt.Errorf("argument %d of %s() is not numeric: %w", i+1, f.Name, err)
		}
		args[i] = fv
	}
	arity := func(want int) error {
		if len(args) != want {
			return fmt.Errorf("%s() takes %d argument(s), got %d", f.Name, want, len(args))
		}
		return nil
	}
	switch f.Name {
	case "abs":
		if err := arity(1); err != nil {
			return nil, err
		}
		return math.Abs(args[0]), nil
	case "sqrt":
		if err := arity(1); err != nil {
			return nil, err
		}
		return math.Sqrt(args[0]), nil
	case "ceil":
		if err := arity(1); err != nil {
			return nil, err
		}
		return math.Ceil(args[0]), nil
	case "floor":
		if err := arity(1); err != nil {
			return nil, err
		}
		return math.Floor(args[0]), nil
	case "round":
		switch len(args) {
		case 1:
			return math.Round(args[0]), nil
		case 2:
			p := math.Pow(10, args[1])
			return math.Round(args[0]*p) / p, nil
		default:
			return nil, fmt.Errorf("round() takes 1 or 2 arguments, got %d", len(args))
		}
	case "pow":
		if err := arity(2); err != nil {
			return nil, err
		}
		return math.Pow(args[0], args[1]), nil
	case "min", "max":
		if len(args) == 0 {
			return nil, fmt.Errorf("%s() needs at least one argument", f.Name)
		}
		res := args[0]
		for _, v := range args[1:] {
			if (f.Name == "min" && v < res) || (f.Name == "max" && v > res) {
				res = v
			}
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unknown function %s()", f.Name)
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
