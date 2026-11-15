# Proposal: chat-focus-backend

## Why
Parent feature junction-2026/project#93 (child junction-2026/backend#164) lets the client focus the VEIL chat on
selected analysis result elements. The shared contract is analysis-chat-api change `chat-focus`
(shared-specs@36b794f7acd8b31b757f00aeefc57c9f27d7f4d3). This change holds the backend-internal behaviour: parsing,
resolution against the stored job, the extra server-owned prompt message, its cap and logging.

## What Changes
- `parseChat` parses and validates `focus` into `[]chat.FocusItem` together with the messages errors (one 422).
- `chat.Service.Reply` takes the focus items; without items the upstream messages are unchanged.
- New `internal/chat/focus.go`: resolution of every shared kind against the stored job, dedupe, and the focus
  system message (JSON between `<<<VEIL_FOCUS_DATA` / `VEIL_FOCUS_DATA>>>`, at most 16000 code points).
- The chat log line adds `focus_count`; the persona states that focus data is untrusted.

## Impact
- Code: `internal/httpapi/chat.go`, `internal/chat/{chat.go,ground.go,focus.go,persona.txt}` and tests.
- No API change beyond the shared contract; no storage change; ChatReply unchanged.
