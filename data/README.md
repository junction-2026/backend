# data/

## outlets.csv

News outlets with their domains, country and political leaning: one row per outlet, about 5,350 rows, as of
2026-11-14. It is a static snapshot: change it through a pull request, and keep the rules below.
`outlets_test.go` checks them, so `go test ./data/` must pass after any edit.

| Column | Meaning |
|---|---|
| `domain` | the outlet's main registered domain, lower case, no `www.` (e.g. `bbc.co.uk`); never empty |
| `domains` | all of the outlet's domains, `\|`-separated, always including `domain` (e.g. `bbc.co.uk\|bbc.com`) |
| `hosts` | exact hostnames the outlet's articles were seen on, `\|`-separated, lower case, no `www.` (e.g. `sportstar.thehindu.com`); may be empty |
| `name` | display name; never empty |
| `country_code` | ISO 3166-1 alpha-2, upper case; empty when unknown (~45% of rows) |
| `country` | country name; set exactly when `country_code` is set |
| `bias` | political leaning: `farLeft`, `left`, `leanLeft`, `center`, `leanRight`, `right`, `farRight` or `unknown` (~65% `unknown`) |
| `factuality` | factual reporting rating: `veryLow`, `low`, `mixed`, `high`, `veryHigh` or `unknown` |
| `site_lang` | site language, ISO 639 code in lower case (e.g. `en`, `es`); empty when unknown |

Every value in `domains` and `hosts` belongs to exactly one outlet, so a lookup by exact host, then by
registered domain, gives at most one outlet. Rows are ordered by outlet size, largest first.
