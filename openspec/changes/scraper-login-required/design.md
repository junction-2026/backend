# Design

- `Module.loginRequired()` and `Module.notFound(what)` in the Instagram module are the only places that build the
  login and not-found errors, so the HTTP 404 path and the in-body empty-answer paths cannot diverge. The session
  is the only input: `m.session == nil` means anonymous.
- A failing feed or comments request keeps its existing warning behaviour; the warning text is the error message,
  which names no setting value.
- Threads `base()` is kept as a function returning the constant origin; the URL host only selects the platform.
- Threads login detection (`loginWall`) runs only on a 200 page that lacks the wanted data, so post text that
  mentions "login" never turns a good page into an error. Markers: `<input type=password>`, `<form action>` or
  `<a href>` whose path is `/login` or under `/login/`, and JSON objects with `require_login: true` or
  `message`/`error_type` `login_required`.
- Threads redirects stay unfollowed and remain `upstream_error`.
