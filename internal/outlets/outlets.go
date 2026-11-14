// Package outlets loads the outlet table (data/outlets.csv) and matches URLs to outlets, offline.
package outlets

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Header is the required first row of the outlet table.
var Header = []string{"domain", "domains", "hosts", "name", "country_code", "country", "bias", "factuality", "site_lang"}

// Outlet is the Outlet object of source-info-result; hosts and site_lang are not part of it.
type Outlet struct {
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	CountryCode string `json:"country_code"`
	Country     string `json:"country"`
	Bias        string `json:"bias"`
	Factuality  string `json:"factuality"`
}

// Table is immutable after Load and safe for concurrent reads.
type Table struct {
	Source string // file name without directory
	Count  int
	hosts  map[string]*Outlet // every outlet's hosts and domains
	byDom  map[string]*Outlet // every outlet's domains
}

// Load reads and validates the CSV at path. Errors name the file and the 1-based row.
func Load(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // checked per row so the error names the row
	t := &Table{Source: filepath.Base(path), hosts: map[string]*Outlet{}, byDom: map[string]*Outlet{}}
	seenHost := map[string]bool{}
	for row := 1; ; row++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			if row == 1 {
				return nil, fmt.Errorf("%s row 1: header must be %s", path, strings.Join(Header, ","))
			}
			return t, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", path, row, err)
		}
		if row == 1 {
			if strings.Join(rec, ",") != strings.Join(Header, ",") {
				return nil, fmt.Errorf("%s row 1: header must be %s", path, strings.Join(Header, ","))
			}
			continue
		}
		if len(rec) != len(Header) {
			return nil, fmt.Errorf("%s row %d: want %d columns, got %d", path, row, len(Header), len(rec))
		}
		o := &Outlet{Domain: strings.ToLower(strings.TrimSpace(rec[0])), Name: rec[3], CountryCode: rec[4], Country: rec[5], Bias: rec[6], Factuality: rec[7]}
		if o.Domain == "" {
			return nil, fmt.Errorf("%s row %d: domain is empty", path, row)
		}
		// a value may repeat inside one outlet (domain is usually also in domains); not across outlets
		own := map[string]bool{}
		for _, v := range split(rec[1]) {
			if err := claim(seenHost, own, v, path, row); err != nil {
				return nil, err
			}
			t.hosts[v], t.byDom[v] = o, o
		}
		for _, v := range split(rec[2]) {
			if err := claim(seenHost, own, v, path, row); err != nil {
				return nil, err
			}
			t.hosts[v] = o
		}
		for v := range own {
			seenHost[v] = true
		}
		t.Count++
	}
}

// claim fails when v was seen under another outlet; own collects this row's values.
func claim(seen, own map[string]bool, v, path string, row int) error {
	if seen[v] {
		return fmt.Errorf("%s row %d: %q is already listed under another outlet", path, row, v)
	}
	own[v] = true
	return nil
}

// split: |-separated, trimmed, lower-cased, without a leading www., empty items ignored.
func split(s string) []string {
	var out []string
	for _, v := range strings.Split(s, "|") {
		if v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "www."); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Host is the normalized host of a URL: no port, lower case, no trailing dot, one leading www. removed;
// "" when the URL has no parsable host.
func Host(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return strings.TrimPrefix(h, "www.")
}

// Match returns the outlet of a normalized host and how it matched ("host" or "domain"); nil when unmatched.
func (t *Table) Match(host string) (*Outlet, string) {
	if o := t.hosts[host]; o != nil {
		return o, "host"
	}
	if net.ParseIP(host) != nil {
		return nil, ""
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		if o := t.byDom[d]; o != nil {
			return o, "domain"
		}
	}
	return nil, ""
}
