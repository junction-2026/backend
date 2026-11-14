package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Handler is an in-process analyzer; its result must marshal to a JSON object.
type Handler func(ctx context.Context, req Request) (any, error)

// Handlers is the in-process handler registry keyed by name.
type Handlers struct{ m map[string]Handler }

// NewHandlers returns a registry holding the built-in handlers.
func NewHandlers() *Handlers {
	h := &Handlers{m: map[string]Handler{}}
	h.Register("text_stats", textStats)
	return h
}

func (h *Handlers) Register(name string, fn Handler) { h.m[name] = fn }
func (h *Handlers) Has(name string) bool             { _, ok := h.m[name]; return ok }

// textStats returns {characters, words, lines} of normalized_content, read
// from the first input that is a $input object or a plain string.
func textStats(_ context.Context, req Request) (any, error) {
	var s string
	for _, v := range req.Inputs {
		switch t := v.(type) {
		case map[string]any:
			if n, ok := t["normalized_content"].(string); ok {
				s = n
			}
		case string:
			s = t
		}
		if s != "" {
			break
		}
	}
	lines := 0
	if s != "" {
		lines = strings.Count(s, "\n") + 1
	}
	return map[string]int{"characters": utf8.RuneCountInString(s), "words": len(strings.Fields(s)), "lines": lines}, nil
}

func (x *Executor) custom(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	fn := x.Handlers.m[def.Execution.Handler]
	if fn == nil {
		return nil, adapterErr("custom handler %q is not registered", def.Execution.Handler)
	}
	type res struct {
		v   any
		err error
	}
	ch := make(chan res, 1) // buffered: a handler that ignores ctx must not leak blocked
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- res{err: fmt.Errorf("handler panicked: %v", p)}
			}
		}()
		v, err := fn(ctx, req)
		ch <- res{v, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ContextError(ctx)
	case r := <-ch:
		if r.err != nil {
			var re *job.RunError
			if errors.As(r.err, &re) {
				return nil, re
			}
			if e := ContextError(ctx); e != nil {
				return nil, e
			}
			return nil, adapterErr("%v", r.err)
		}
		b, err := json.Marshal(r.v)
		if err != nil || len(b) == 0 || b[0] != '{' {
			return nil, adapterErr("custom handler result is not a JSON object")
		}
		return b, nil
	}
}
