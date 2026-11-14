package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The stub LLM answers per post as a model following config/extractor/prompt.md would; the test checks that
// the real prompt file is the system message, carries the rules and examples, and that the adapter passes
// short queries through unchanged.
func TestExtractorPromptShortQueriesAndPlans(t *testing.T) {
	b, err := os.ReadFile("../../config/extractor/prompt.md")
	require.NoError(t, err)
	prompt := string(b)
	for _, want := range []string{
		"2-4 words", "dates and years", "relative", "verbs of deciding or saying", "hedges", "framing", "infinitive or noun form",
		"decided, plans, wants, announced or will do", "forecasts about", "uncertain outcomes nobody controls",
		`"query": "pension age 70"`, `"query": "EU Bargeld abschaffen", "query_en": "EU abolish cash"`,
		"Die EU hat gestern beschlossen, das Bargeld ab 2027 komplett abzuschaffen.",
		"Die EU will das Bargeld 2027 abschaffen", "Die EU plant, das Bargeld 2027 abzuschaffen.",
		"The economy will collapse next year, wake up", "politicians are all the same, wake up",
	} {
		require.Contains(t, prompt, want)
	}

	replies := map[string]string{
		"EILMELDUNG: Die EU hat gestern beschlossen, das Bargeld ab 2027 komplett abzuschaffen!": `{"topics":[],"news":[],"claims":[` +
			`{"text":"Die EU hat gestern beschlossen, das Bargeld ab 2027 komplett abzuschaffen.","language":"de","query":"EU Bargeld abschaffen",` +
			`"query_en":"EU abolish cash","entities":["EU","Bargeld","2027"],"check_worthiness":0.9}]}`,
		"Die EU will das Bargeld 2027 abschaffen": `{"topics":[],"news":[],"claims":[` +
			`{"text":"Die EU plant, das Bargeld 2027 abzuschaffen.","language":"de","query":"EU Bargeld abschaffen",` +
			`"query_en":"EU abolish cash","entities":["EU","Bargeld","2027"],"check_worthiness":0.7}]}`,
		"The economy will collapse next year, wake up": `{"topics":[],"news":[],"claims":[]}`,
	}
	var system string
	m := newTopicMock(t, func(w http.ResponseWriter, r *http.Request) {})
	m.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		system = body.Messages[0].Content
		user := body.Messages[1].Content
		for post, reply := range replies {
			if strings.Contains(user, post) {
				w.Write([]byte(topicChat(reply)))
				return
			}
		}
		t.Errorf("unexpected post %q", user)
	})
	d := topicDef(m.URL)
	d.Execution.Prompt = prompt

	run := func(post string) []claim {
		got, err := runTopic(context.Background(), d, post)
		require.NoError(t, err)
		require.Equal(t, prompt, system, "the prompt file is the system message")
		var res struct{ Claims []claim }
		require.NoError(t, json.Unmarshal(got, &res))
		return res.Claims
	}

	cs := run("EILMELDUNG: Die EU hat gestern beschlossen, das Bargeld ab 2027 komplett abzuschaffen!")
	require.Len(t, cs, 1)
	for _, q := range []string{cs[0].Query, cs[0].QueryEN} {
		require.LessOrEqual(t, len(strings.Fields(q)), 4)
		for _, bad := range []string{"2027", "gestern", "beschließt", "Eilmeldung"} {
			require.NotContains(t, strings.ToLower(q), strings.ToLower(bad))
		}
	}
	require.Contains(t, cs[0].Text, "gestern", "text keeps the full claim")

	require.Len(t, run("Die EU will das Bargeld 2027 abschaffen"), 1, "a plan is a claim")
	require.Empty(t, run("The economy will collapse next year, wake up"), "a forecast is not")
}
