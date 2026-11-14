package facebook

import (
	"context"
	"sync"
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/stretchr/testify/assert"
)

// Network events racing Bodies() must be safe (run under -race). The context is not a chromedp one, so every
// body read fails at once: this exercises the in-flight accounting, not chromium.
func TestEventsRaceWithBodies(t *testing.T) {
	c := newChrome(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		id := network.RequestID(string(rune('a' + i%26)))
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.onEvent(&network.EventResponseReceived{RequestID: id, Response: &network.Response{URL: "https://www.facebook.com/api/graphql/"}})
			c.onEvent(&network.EventLoadingFinished{RequestID: id})
		}()
		go func() { defer wg.Done(); c.Bodies() }()
	}
	wg.Wait()
	assert.Empty(t, c.Bodies())
	assert.Zero(t, c.inflight)
}
