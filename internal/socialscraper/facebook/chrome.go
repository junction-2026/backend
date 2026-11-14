package facebook

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const (
	settle     = 1500 * time.Millisecond // Facebook never goes network-idle; wait a fixed time for its GraphQL calls
	textBudget = 2 * time.Second
)

// chrome is the real Browser: one chromium process driven through chromedp.
type chrome struct {
	ctx            context.Context
	cancelBrowser  context.CancelFunc
	cancelAllocate context.CancelFunc

	mu      sync.Mutex
	pending sync.WaitGroup
	graphql map[network.RequestID]bool // GraphQL responses whose body is not yet loadable
	bodies  [][]byte
	status  int
}

// launchChrome starts chromium from PATH. Under `go test` it panics: a test must install a fake launcher,
// so a forgotten fake fails the test instead of starting a browser.
func launchChrome(ctx context.Context) (Browser, error) {
	if testing.Testing() {
		panic("facebook: the real chromedp launcher was reached under test; install a fake Launch")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("lang", "en-US"),
	)
	actx, cancelA := chromedp.NewExecAllocator(ctx, opts...)
	bctx, cancelB := chromedp.NewContext(actx)
	c := &chrome{ctx: bctx, cancelBrowser: cancelB, cancelAllocate: cancelA, graphql: map[network.RequestID]bool{}}
	chromedp.ListenTarget(bctx, c.onEvent)
	if err := chromedp.Run(bctx, network.Enable()); err != nil { // starts the browser
		c.Close()
		return nil, err
	}
	return c, nil
}

// onEvent runs on the CDP event loop and must not block: bodies are fetched in goroutines.
func (c *chrome) onEvent(ev any) {
	switch e := ev.(type) {
	case *network.EventResponseReceived:
		c.mu.Lock()
		defer c.mu.Unlock()
		if e.Type == network.ResourceTypeDocument && c.status == 0 {
			c.status = int(e.Response.Status)
		}
		if isGraphQL(e.Response.URL) {
			c.graphql[e.RequestID] = true
		}
	case *network.EventLoadingFinished:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.graphql[e.RequestID] {
			delete(c.graphql, e.RequestID)
			c.pending.Add(1)
			go func() {
				defer c.pending.Done()
				c.readBody(e.RequestID)
			}()
		}
	}
}

// isGraphQL accepts only the page's own https://www.facebook.com/api/graphql/ calls.
func isGraphQL(u string) bool {
	return u == "https://www.facebook.com/api/graphql" || strings.HasPrefix(u, "https://www.facebook.com/api/graphql/")
}

func (c *chrome) readBody(id network.RequestID) {
	var body []byte
	err := chromedp.Run(c.ctx, chromedp.ActionFunc(func(ctx context.Context) (err error) {
		body, err = network.GetResponseBody(id).Do(ctx)
		return err
	}))
	if err != nil || len(body) > ss.MaxResponseBytes {
		return
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
}

func (c *chrome) Navigate(url string) (int, error) {
	resp, err := chromedp.RunResponse(c.ctx, chromedp.Navigate(url))
	if err != nil {
		return 0, err
	}
	if err := c.sleep(settle); err != nil {
		return 0, err
	}
	if resp != nil {
		return int(resp.Status), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, nil
}

func (c *chrome) Scroll() error {
	if err := chromedp.Run(c.ctx, chromedp.Evaluate("window.scrollBy(0, 6000)", nil)); err != nil {
		return err
	}
	return c.sleep(time.Second)
}

func (c *chrome) sleep(d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *chrome) Bodies() [][]byte {
	c.pending.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.bodies...)
}

func (c *chrome) Text() string {
	ctx, cancel := context.WithTimeout(c.ctx, textBudget)
	defer cancel()
	var s string
	if err := chromedp.Run(ctx, chromedp.Text("body", &s, chromedp.ByQuery)); err != nil {
		return ""
	}
	return s
}

// Close stops chromium and waits for the process to exit; calling it again is harmless.
func (c *chrome) Close() {
	_ = chromedp.Cancel(c.ctx) // waits for the process; an error leaves nothing to do, the cancels below force it
	c.cancelBrowser()
	c.cancelAllocate()
}
