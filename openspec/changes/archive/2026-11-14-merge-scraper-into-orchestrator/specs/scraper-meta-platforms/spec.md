## MODIFIED Requirements

### Requirement: Meta platform registration and client use
`cmd/orchestrator` SHALL register the Instagram module for `instagram` (with `INSTAGRAM_SESSION_FILE`) and the Threads module for `threads` when it builds the scraper registry at startup. Both modules SHALL use only the shared `*http.Client` injected from the core and SHALL run under the scrape context.

#### Scenario: Scrape through the server
- **WHEN** jobs are created with `https://www.instagram.com/reel/C1a2b3c4d5/` and `https://www.threads.net/@alice` against the orchestrator with routed fixtures
- **THEN** both scrapes succeed with `platform` `instagram` / `threads` and `url_kind` `post` / `profile`
