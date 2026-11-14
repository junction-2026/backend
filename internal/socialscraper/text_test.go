package socialscraper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		`<p>Hello &amp; <a href="https://x.y">welcome</a></p><p>Bye</p>`: "Hello & welcome\nBye",
		"  <p>a<br>b<br/>c &lt;3 &#39;q&#39;</p>\n":                      "a\nb\nc <3 'q'",
		"":                   "",
		"<p></p>":            "",
		"plain & simple":     "plain & simple",
		"<p>one</p>\n<p>two": "one\n\ntwo",
	} {
		assert.Equal(t, want, PlainText(in), in)
	}
}

func TestTimestamp(t *testing.T) {
	assert.Equal(t, "2026-11-14T09:30:15Z", *Timestamp("2026-11-14T09:30:15.123Z"))
	assert.Equal(t, "2026-11-14T09:30:15Z", *Timestamp("2026-11-14T11:30:15+02:00"))
	assert.Equal(t, "2026-11-14T09:30:15Z", *Timestamp("2026-11-14T09:30:15"))
	assert.Nil(t, Timestamp("garbage"))
	assert.Nil(t, Timestamp(""))
}
