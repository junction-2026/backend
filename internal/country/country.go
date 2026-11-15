// Package country holds the ISO 3166-1 alpha-2 country table (officially assigned codes with English short names)
// the country association step validates codes and takes names from (shared capability country-association).
package country

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
)

//go:embed iso3166-1.csv
var isoCSV []byte

// Table maps upper-case alpha-2 codes to English short names.
type Table struct{ names map[string]string }

// Load parses the shipped table; the server refuses to start when it fails.
func Load() (*Table, error) { return Parse(isoCSV) }

// Parse reads a "code,name" CSV with a header row: every code two upper-case ASCII letters, unique, name non-empty.
func Parse(b []byte) (*Table, error) {
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = 2
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("country table: %w", err)
	}
	if len(recs) < 2 || recs[0][0] != "code" || recs[0][1] != "name" {
		return nil, errors.New("country table: header code,name and at least one row expected")
	}
	t := &Table{names: make(map[string]string, len(recs)-1)}
	for i, rec := range recs[1:] {
		code, name := rec[0], strings.TrimSpace(rec[1])
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' || name == "" {
			return nil, fmt.Errorf("country table: invalid row %d", i+2)
		}
		if _, dup := t.names[code]; dup {
			return nil, fmt.Errorf("country table: duplicate code %s", code)
		}
		t.names[code] = name
	}
	return t, nil
}

// Name returns the upper-cased code and its name when code (any case, surrounding space ignored) is in the table.
func (t *Table) Name(code string) (string, string, bool) {
	c := strings.ToUpper(strings.TrimSpace(code))
	n, ok := t.names[c]
	return c, n, ok
}

// Len is the number of codes.
func (t *Table) Len() int { return len(t.names) }
