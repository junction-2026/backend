package outlets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const hdr = "domain,domains,hosts,name,country_code,country,bias,factuality,site_lang\n"

// fiveRows is the 5-row table of the matching tests.
const fiveRows = hdr +
	"bbc.co.uk,bbc.co.uk|bbc.com,bbc.co.uk|bbc.com,BBC News,GB,United Kingdom,center,veryHigh,en\n" +
	"thehindu.com,thehindu.com,sportstar.thehindu.com,The Hindu,IN,India,leanLeft,high,en\n" +
	"spiegel.de,spiegel.de,,Der Spiegel,DE,Germany,leanLeft,high,de\n" +
	"example-blog.org,,,Some Blog,,,unknown,unknown,\n" +
	"foxnews.com,foxnews.com| WWW.Fox.Example ,,Fox News,US,United States,right,mixed,en\n"

func write(t *testing.T, name, content string) string {
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestMatch(t *testing.T) {
	tb, err := Load(write(t, "outlets.csv", fiveRows))
	require.NoError(t, err)
	require.Equal(t, 5, tb.Count)
	require.Equal(t, "outlets.csv", tb.Source)
	for _, c := range []struct{ url, host, name, match string }{
		{"https://news.bbc.co.uk/x", "news.bbc.co.uk", "BBC News", "domain"},
		{"https://sportstar.thehindu.com/x", "sportstar.thehindu.com", "The Hindu", "host"},
		{"https://WWW.BBC.COM./x", "bbc.com", "BBC News", "host"},
		{"https://www.spiegel.de:8443/a", "spiegel.de", "Der Spiegel", "host"},
		{"https://fox.example/a", "fox.example", "Fox News", "host"},
		{"https://unknown.example/x", "unknown.example", "", ""},
		{"http://10.1.2.3/x", "10.1.2.3", "", ""},
		{"::not a url", "", "", ""},
	} {
		host := Host(c.url)
		require.Equal(t, c.host, host, c.url)
		o, match := tb.Match(host)
		require.Equal(t, c.match, match, c.url)
		if c.name == "" {
			require.Nil(t, o, c.url)
		} else {
			require.Equal(t, c.name, o.Name, c.url)
		}
	}
}

func TestLoadValidation(t *testing.T) {
	for name, c := range map[string]struct{ csv, want string }{
		"wrong header":          {"domain,name\nbbc.com,BBC\n", "row 1"},
		"empty file":            {"", "row 1"},
		"empty domain":          {hdr + "a.com,a.com,,A,,,unknown,unknown,\n,b.com,,B,,,unknown,unknown,\n", "row 3: domain is empty"},
		"duplicate host":        {hdr + "a.com,a.com,x.a.com,A,,,unknown,unknown,\nb.com,b.com,x.a.com|y.b.com,B,,,unknown,unknown,\n", `row 3: "x.a.com"`},
		"duplicate domain case": {hdr + "a.com,a.com,,A,,,unknown,unknown,\nb.com,B.com|A.COM,,B,,,unknown,unknown,\n", `row 3: "a.com"`},
		"short row":             {hdr + "a.com,a.com\n", "row 2"},
	} {
		t.Run(name, func(t *testing.T) {
			p := write(t, "o.csv", c.csv)
			_, err := Load(p)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, p)
		})
	}
	_, err := Load(filepath.Join(t.TempDir(), "missing.csv"))
	require.Error(t, err)
}

func TestRealTable(t *testing.T) {
	tb, err := Load("../../data/outlets.csv")
	require.NoError(t, err)
	require.Greater(t, tb.Count, 5000)
	require.Equal(t, "outlets.csv", tb.Source)
	o, how := tb.Match(Host("https://www.spiegel.de/politik/a-1.html"))
	require.NotNil(t, o)
	require.Equal(t, "host", how)
}
