package scrapertest

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recorder captures failures instead of failing the real test.
type recorder struct {
	testing.TB
	errors []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestUnroutedHostFailsTheTest(t *testing.T) {
	rec := &recorder{TB: t}
	r := New(rec).Handle("routed.example", Body(200, []byte(`{}`)))

	resp, err := r.Client().Get("https://routed.example/x")
	assert.NoError(t, err)
	resp.Body.Close()
	assert.Empty(t, rec.errors)

	_, err = r.Client().Get("https://real-platform.example/x")
	assert.Error(t, err)
	assert.Len(t, rec.errors, 1)
	assert.Contains(t, rec.errors[0], "real-platform.example")
}

func TestHandlerSeesRequestURI(t *testing.T) {
	var got string
	r := New(t).Handle("h.example", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { got = req.RequestURI }))
	resp, err := r.Client().Get("https://h.example/a/b?c=d")
	assert.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, "/a/b?c=d", got)
}
