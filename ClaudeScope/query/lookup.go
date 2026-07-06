package query

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

// loadLookupTables reads a CSV lookup file (header row required) and builds
// one keyValue -> outputValue map per requested output column, keyed by the
// column name. Loaded eagerly at parse time so a missing file or bad column
// name surfaces as an immediate parse error rather than a mid-run failure.
func loadLookupTables(path, keyField string, outputCols []string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("lookup: cannot open %q: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("lookup: cannot read header row from %q: %w", path, err)
	}
	colIndex := make(map[string]int, len(header))
	for i, h := range header {
		colIndex[h] = i
	}
	keyIdx, ok := colIndex[keyField]
	if !ok {
		return nil, fmt.Errorf("lookup: %q has no column %q", path, keyField)
	}
	outIdx := make(map[string]int, len(outputCols))
	tables := make(map[string]map[string]string, len(outputCols))
	for _, oc := range outputCols {
		idx, ok := colIndex[oc]
		if !ok {
			return nil, fmt.Errorf("lookup: %q has no column %q", path, oc)
		}
		outIdx[oc] = idx
		tables[oc] = map[string]string{}
	}

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("lookup: error reading %q: %w", path, err)
		}
		key := rec[keyIdx]
		for _, oc := range outputCols {
			tables[oc][key] = rec[outIdx[oc]]
		}
	}
	return tables, nil
}
