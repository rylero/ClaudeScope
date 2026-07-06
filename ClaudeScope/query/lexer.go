package query

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type tokKind int

const (
	tIdent tokKind = iota
	tNumber
	tString
	tPipe
	tComma
	tLParen
	tRParen
	tGT
	tLT
	tGE
	tLE
	tEQ
	tNE
	tMinus
	tEOF
)

type token struct {
	kind tokKind
	text string
	num  float64
}

// lex tokenizes an entire pipe query string, e.g.
// `where CurrentA > 40 | stats avg(BatteryVoltage) by Subsystem`.
func lex(input string) ([]token, error) {
	var toks []token
	r := []rune(input)
	i, n := 0, len(r)
	for i < n {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '|':
			toks = append(toks, token{kind: tPipe})
			i++
		case c == ',':
			toks = append(toks, token{kind: tComma})
			i++
		case c == '(':
			toks = append(toks, token{kind: tLParen})
			i++
		case c == ')':
			toks = append(toks, token{kind: tRParen})
			i++
		case c == '-':
			toks = append(toks, token{kind: tMinus})
			i++
		case c == '>':
			if i+1 < n && r[i+1] == '=' {
				toks = append(toks, token{kind: tGE})
				i += 2
			} else {
				toks = append(toks, token{kind: tGT})
				i++
			}
		case c == '<':
			if i+1 < n && r[i+1] == '=' {
				toks = append(toks, token{kind: tLE})
				i += 2
			} else {
				toks = append(toks, token{kind: tLT})
				i++
			}
		case c == '=':
			if i+1 < n && r[i+1] == '=' {
				toks = append(toks, token{kind: tEQ})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected '=' at position %d (did you mean '=='?)", i)
			}
		case c == '!':
			if i+1 < n && r[i+1] == '=' {
				toks = append(toks, token{kind: tNE})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected '!' at position %d", i)
			}
		case c == '"' || c == '\'':
			quote := c
			j := i + 1
			var sb strings.Builder
			for j < n && r[j] != quote {
				sb.WriteRune(r[j])
				j++
			}
			if j >= n {
				return nil, fmt.Errorf("unterminated string literal")
			}
			toks = append(toks, token{kind: tString, text: sb.String()})
			i = j + 1
		case unicode.IsDigit(c):
			j := i
			for j < n && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			numStr := string(r[i:j])
			f, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid number %q", numStr)
			}
			toks = append(toks, token{kind: tNumber, num: f, text: numStr})
			i = j
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(r[j]) {
				j++
			}
			toks = append(toks, token{kind: tIdent, text: string(r[i:j])})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", c, i)
		}
	}
	toks = append(toks, token{kind: tEOF})
	return toks, nil
}

func isIdentStart(c rune) bool {
	return unicode.IsLetter(c) || c == '_' || c == '/'
}

func isIdentPart(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '/' || c == '.' || c == '-'
}
