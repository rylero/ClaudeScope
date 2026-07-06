package query

import (
	"fmt"
	"math"
	"sort"

	"github.com/rylero/TheFRCSuite/ClaudeScope/session"
)

var aggPercentiles = map[string]float64{"p50": 0.5, "p90": 0.9, "p99": 0.99}

// computeAgg reduces vals (already-numeric, in row order) into a single
// aggregate value for fn. rowCount is the group's row count, used by "count"
// which doesn't require a numeric field.
func computeAgg(fn string, vals []float64, rowCount int) (float64, error) {
	switch fn {
	case "count":
		return float64(rowCount), nil
	case "sum":
		var s float64
		for _, v := range vals {
			s += v
		}
		return s, nil
	case "avg":
		if len(vals) == 0 {
			return 0, nil
		}
		var s float64
		for _, v := range vals {
			s += v
		}
		return s / float64(len(vals)), nil
	case "min":
		if len(vals) == 0 {
			return 0, nil
		}
		m := vals[0]
		for _, v := range vals[1:] {
			if v < m {
				m = v
			}
		}
		return m, nil
	case "max":
		if len(vals) == 0 {
			return 0, nil
		}
		m := vals[0]
		for _, v := range vals[1:] {
			if v > m {
				m = v
			}
		}
		return m, nil
	case "median":
		return session.Percentile(sortedCopy(vals), 0.5), nil
	case "stdev":
		return stdev(vals), nil
	default:
		if p, ok := aggPercentiles[fn]; ok {
			return session.Percentile(sortedCopy(vals), p), nil
		}
		return 0, fmt.Errorf("unknown aggregate function %q", fn)
	}
}

func sortedCopy(vals []float64) []float64 {
	out := make([]float64, len(vals))
	copy(out, vals)
	sort.Float64s(out)
	return out
}

func stdev(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	var mean float64
	for _, v := range vals {
		mean += v
	}
	mean /= float64(len(vals))
	var sumSq float64
	for _, v := range vals {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)-1))
}
