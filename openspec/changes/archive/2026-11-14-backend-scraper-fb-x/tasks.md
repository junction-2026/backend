# Tasks

## 1. Facebook
- [x] 1.1 Recognizer moved from `pending.py`; `facebook.py` with Playwright, GraphQL interception, scrolling history, post comments
- [x] 1.2 Personal-profile detection, not-found mapping, timeout and browser-closed-on-every-path handling
- [x] 1.3 Fixtures `tests/fixtures/facebook/` and `tests/test_facebook.py` with a fake Playwright

## 2. X
- [x] 2.1 Recognizer moved from `pending.py`; `x.py` with twscrape pool from `TWITTER_AUTH_TOKEN`/`TWITTER_CT0`
- [x] 2.2 Profile, post, history, replies, error mapping, `platform_not_configured` with names only
- [x] 2.3 Fixtures `tests/fixtures/x/` and `tests/test_x.py` incl. sentinel credential test

## 3. Wiring
- [x] 3.1 Registry entries, `pyproject.toml` dependencies, `Dockerfile` chromium install, README
- [x] 3.2 Autouse test guard against real browser and real X access
