# data/

## outlets.csv

News outlets with their domains, country and political leaning: one row per outlet, 5,353 rows, compiled
2026-11-14. Regenerate the file rather than editing it by hand.

| Column | Meaning |
|---|---|
| `domain` | the outlet's main registered domain, lower case, no `www.` (e.g. `bbc.co.uk`); never empty |
| `domains` | all of the outlet's domains, `\|`-separated, always including `domain` (e.g. `bbc.co.uk\|bbc.com`) |
| `hosts` | exact hostnames the outlet's articles were seen on, `\|`-separated, no `www.` (e.g. `sportstar.thehindu.com`); may be empty |
| `name` | display name |
| `country_code` | ISO 3166-1 alpha-2, upper case; empty when unknown (~44% of rows) |
| `country` | country name; empty when unknown |
| `bias` | political leaning: `farLeft`, `left`, `leanLeft`, `center`, `leanRight`, `right`, `farRight` or `unknown` (~65% `unknown`) |
| `factuality` | factual reporting rating: `veryLow`, `low`, `mixed`, `high`, `veryHigh` or `unknown` |
| `site_lang` | site language code (e.g. `en`, `es`); empty when unknown |

Every value in `domains` and `hosts` belongs to exactly one outlet, so a lookup by exact host, then by
registered domain, gives at most one outlet. Rows are ordered by outlet size, largest first.
