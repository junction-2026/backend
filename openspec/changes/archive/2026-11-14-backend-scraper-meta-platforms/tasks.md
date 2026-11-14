# Tasks

## 1. Instagram
- [x] 1.1 `platforms/instagram.py`: recognizer, anonymous instaloader loader, optional session file
- [x] 1.2 Profile, post, history and comments mapping; unknown counters are null
- [x] 1.3 Failure mapping (private, not found, login/429, timeout) and partial-result warnings

## 2. Threads
- [x] 2.1 `platforms/threads.py`: recognizer, httpx fetch with browser User-Agent
- [x] 2.2 Defensive SSR JSON extraction (profile, posts, comments), warnings, `upstream_error` on unparseable pages

## 3. Wiring, tests, docs
- [x] 3.1 Registry entries replace the Pending ones; `instaloader` dependency
- [x] 3.2 Recorded fixtures and offline tests for both modules; socket guard
- [x] 3.3 README
