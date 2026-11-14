package data

import (
	"encoding/csv"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The rules data/README.md documents for outlets.csv.
var (
	header     = []string{"domain", "domains", "hosts", "name", "country_code", "country", "bias", "factuality", "site_lang"}
	hostRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	countryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
	langRe     = regexp.MustCompile(`^[a-z]{2,3}$`)
	bias       = []string{"farLeft", "left", "leanLeft", "center", "leanRight", "right", "farRight", "unknown"}
	factuality = []string{"veryLow", "low", "mixed", "high", "veryHigh", "unknown"}
)

func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "|")
}

func TestOutletsCSV(t *testing.T) {
	f, err := os.Open("outlets.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = len(header)
	recs, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(recs[0], header) {
		t.Fatalf("header = %v, want %v", recs[0], header)
	}
	if n := len(recs) - 1; n < 5000 {
		t.Fatalf("%d outlets, want at least 5000", n)
	}
	owner := map[string]int{} // domain or host -> line
	for i, rec := range recs[1:] {
		line := i + 2
		domain, domains, hosts, name, cc, country, b, fact, lang := rec[0], split(rec[1]), split(rec[2]), rec[3], rec[4], rec[5], rec[6], rec[7], rec[8]
		if name == "" {
			t.Errorf("line %d: empty name", line)
		}
		if !hostRe.MatchString(domain) || strings.HasPrefix(domain, "www.") {
			t.Errorf("line %d: bad domain %q", line, domain)
		}
		if !slices.Contains(domains, domain) {
			t.Errorf("line %d: domains %v lacks domain %q", line, domains, domain)
		}
		for _, h := range append(slices.Clone(domains), hosts...) {
			if !hostRe.MatchString(h) || strings.HasPrefix(h, "www.") {
				t.Errorf("line %d: bad domain or host %q", line, h)
			}
			if prev, ok := owner[h]; ok && prev != line {
				t.Errorf("line %d: %q already belongs to the outlet on line %d", line, h, prev)
			}
			owner[h] = line
		}
		if (cc == "") != (country == "") {
			t.Errorf("line %d: country_code %q and country %q must be both set or both empty", line, cc, country)
		}
		if cc != "" && !countryRe.MatchString(cc) {
			t.Errorf("line %d: bad country_code %q", line, cc)
		}
		if !slices.Contains(bias, b) {
			t.Errorf("line %d: bad bias %q", line, b)
		}
		if !slices.Contains(factuality, fact) {
			t.Errorf("line %d: bad factuality %q", line, fact)
		}
		if lang != "" && !langRe.MatchString(lang) {
			t.Errorf("line %d: bad site_lang %q", line, lang)
		}
	}
}
