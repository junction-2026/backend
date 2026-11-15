package finalizer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

const (
	deText = "Dieser Text ist nicht neutral, weil er den Lesern Angst macht und die Fakten nicht mit der nötigen Vorsicht zeigt."
	enText = "This content is not neutral, because it scares the readers and does not show the facts with the care that is needed."
)

func verdictWith(text string) string {
	b, _ := json.Marshal(map[string]any{"verdict": "detected", "confidence": 0.9, "user_text": text, "wikipedia_quotes": []any{}})
	return chat(string(b))
}

// langJob runs the finalizer for a job whose translation (when lang != "") found lang; extra is appended to the finalizer's execution.
func langJob(t *testing.T, lang, extra string, replies ...string) (job.FinalAssessment, *stub, *job.Job) {
	t.Helper()
	var n int
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, replies[min(n, len(replies)-1)])
		n++
	})
	yaml, sel := llmAnalyzers(), []string{"pd", "claims", "text_stats"}
	hs := map[string]adapter.Handler{}
	if lang != "" {
		yaml += "  - id: translation\n    execution: {adapter: custom, handler: tr}\n"
		hs["tr"] = func(context.Context, adapter.Request) (any, error) {
			return map[string]any{"source_language": lang}, nil
		}
		sel = append(sel, "translation")
	}
	x := newLLMEngineWith(t, yaml+llmFinYAML(extra), s, hs)
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "Hallo"}, Analyzers: sel})
	require.Equal(t, job.StatusCompleted, j.Finalization.Status, "%+v", j.Finalization.Error)
	var a job.FinalAssessment
	require.NoError(t, json.Unmarshal(j.Finalization.Result, &a))
	return a, s, &j
}

func retryReason(a job.FinalAssessment) string {
	if a.RetryReason == nil {
		return "<nil>"
	}
	return *a.RetryReason
}

const wantEn = "user_text must be written in English"

func TestLanguageRetryGermanThenEnglish(t *testing.T) {
	a, s, _ := langJob(t, "", "", verdictWith(deText), verdictWith(enText))
	require.EqualValues(t, 2, s.calls.Load())
	require.Equal(t, enText, a.UserText)
	require.Equal(t, 2, *a.Attempts)
	require.Equal(t, wantEn, retryReason(a))
	r, _ := reqN(t, s, 1)
	require.Len(t, r.Messages, 3)
	require.Contains(t, r.Messages[2].Content, "English")
	require.Contains(t, string(a.Usage), "1024", "usage of both attempts is summed")
}

func TestLanguageRetryNotNeeded(t *testing.T) {
	a, s, _ := langJob(t, "", "", verdictWith(enText))
	require.EqualValues(t, 1, s.calls.Load())
	require.Equal(t, 1, *a.Attempts)
	require.Nil(t, a.RetryReason)
}

func TestLanguageRetryStillWrongKeepsRetryReply(t *testing.T) {
	retry := strings.Replace(deText, "Dieser", "Dieses", 1)
	a, s, _ := langJob(t, "", "", verdictWith(deText), verdictWith(retry))
	require.EqualValues(t, 2, s.calls.Load())
	require.Equal(t, retry, a.UserText)
	require.Equal(t, 2, *a.Attempts)
	require.Equal(t, wantEn, retryReason(a))
}

func TestLanguageRetryInvalidReplyKeepsFirst(t *testing.T) {
	for name, bad := range map[string]string{"not json": chat("no object"), "schema": chat(`{"verdict":"bogus"}`)} {
		t.Run(name, func(t *testing.T) {
			a, s, _ := langJob(t, "", "", verdictWith(deText), bad)
			require.EqualValues(t, 2, s.calls.Load())
			require.Equal(t, deText, a.UserText)
			require.Equal(t, 2, *a.Attempts)
			require.Equal(t, wantEn, retryReason(a))
		})
	}
}

func TestLanguageRetryProviderFailureKeepsFirst(t *testing.T) {
	var n int
	s := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		if n++; n == 1 {
			io.WriteString(w, verdictWith(deText))
			return
		}
		http.Error(w, "boom", http.StatusBadRequest)
	})
	x := newLLMEngine(t, llmAnalyzers()+llmFinYAML(""), s)
	j := runsOf(t, x.e, selected("Hallo"))
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	require.Contains(t, string(j.Finalization.Result), deText)
	require.Contains(t, string(j.Finalization.Result), `"attempts":2`)
}

func TestLanguageRetryAfterSchemaRetryIsNotRepeated(t *testing.T) {
	a, s, _ := langJob(t, "", "", chat(`{"verdict":"detected"}`), verdictWith(deText))
	require.EqualValues(t, 2, s.calls.Load(), "no third request")
	require.Equal(t, deText, a.UserText)
	require.Equal(t, 2, *a.Attempts)
	require.Equal(t, adapter.RetrySchema, retryReason(a))
}

func TestLanguageRetryDisabled(t *testing.T) {
	a, s, _ := langJob(t, "", "      retry_invalid_output: false\n", verdictWith(deText))
	require.EqualValues(t, 1, s.calls.Load())
	require.Equal(t, deText, a.UserText)
	require.Equal(t, 1, *a.Attempts)
	require.Nil(t, a.RetryReason)
}

func TestLanguageRetryExpectedGerman(t *testing.T) {
	a, s, _ := langJob(t, "de", "", verdictWith(enText), verdictWith(deText))
	require.EqualValues(t, 2, s.calls.Load())
	require.Equal(t, deText, a.UserText)
	require.Equal(t, "user_text must be written in German", retryReason(a))
	r, _ := reqN(t, s, 0)
	require.Contains(t, r.Messages[1].Content, "<source_language>de (German)</source_language>")
	a, s, _ = langJob(t, "de", "", verdictWith(deText))
	require.EqualValues(t, 1, s.calls.Load())
	require.Nil(t, a.RetryReason)
}

func TestLanguageRetryNotSentForUnknownLanguageOrShortText(t *testing.T) {
	_, s, _ := langJob(t, "fi", "", verdictWith(enText))
	require.EqualValues(t, 1, s.calls.Load(), "fi is not a language the detector knows")
	_, s, _ = langJob(t, "fi", "", verdictWith(deText))
	require.EqualValues(t, 1, s.calls.Load())
	_, s, _ = langJob(t, "", "", verdictWith("Das ist nicht neutral."))
	require.EqualValues(t, 1, s.calls.Load(), "short text")
}

// The request always carries <source_language>; en (English) when translation did not run.
func TestRequestAlwaysCarriesSourceLanguage(t *testing.T) {
	for lang, want := range map[string]string{"": "en (English)", "de": "de (German)"} {
		_, s, _ := langJob(t, lang, "", verdictWith(map[string]string{"": enText, "de": deText}[lang]))
		r, _ := reqN(t, s, 0)
		require.Contains(t, r.Messages[1].Content, "<source_language>"+want+"</source_language>")
	}
}

// bot_detection results reach the finalizer as they are.
func TestBotDetectionPassesThrough(t *testing.T) {
	const desc = "Single-text classifier; not an account verdict."
	s := newStub(t, reply(verdictWith(enText)))
	yaml := llmAnalyzers() + "  - id: bot_detection\n    execution: {adapter: custom, handler: bot}\n    llm: {include: true, description: \"" + desc + "\"}\n"
	bot := func(context.Context, adapter.Request) (any, error) {
		return map[string]any{"label": "bot_or_generated", "score": 0.95, "scope": "text"}, nil
	}
	x := newLLMEngineWith(t, yaml+llmFinYAML(""), s, map[string]adapter.Handler{"bot": bot})
	j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "Hi"}, Analyzers: []string{"pd", "claims", "text_stats", "bot_detection"}})
	require.Equal(t, job.StatusCompleted, j.Finalization.Status)
	r, _ := reqN(t, s, 0)
	require.Contains(t, r.Messages[1].Content, `{"id":"bot_detection","description":"`+desc+`","status":"succeeded","availability":"ran","result":{"label":"bot_or_generated","scope":"text","score":0.95}}`)
}

func readRepo(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + p)
	require.NoError(t, err)
	return string(b)
}

func TestFinalizerPromptRules(t *testing.T) {
	p := readRepo(t, "config/finalizer/prompt.md")
	flat := strings.Join(strings.Fields(p), " ")
	require.True(t, strings.HasPrefix(p, "You help everyday internet users judge whether a social media post, comment or news article is trying to manipulate them.\n"))
	require.NotContains(t, p, "the post")
	require.NotContains(t, flat, `"propaganda_detection" with the label PROPAGANDA`)
	require.NotContains(t, p, "in the language of the content")
	for _, w := range []string{
		`"propaganda_detection" is a coarse classifier of the whole text`,
		`The label PROPAGANDA supports "detected" only together with another strong signal; on its own it is a weak hint.`,
		`NO_PROPAGANDA does not outweigh a strong signal.`,
		`"bot_detection" says whether this single text looks machine-written or bot-like. It is neither a strong signal nor a weak hint about manipulation, and it never changes the verdict or the confidence.`,
		`A news report that quotes opinions, reports claims attributed to sources, or covers emotional events is not manipulation by itself; judge the article's own framing and wording.`,
		`in the language whose ISO 639-1 code is given in <source_language> (English if it is absent or not a language you can write)`,
		`never mention analyzers, classifiers, models, labels, technique ids, scores or confidence values`,
		`Name a technique by its plain name with the phrase "the text uses"`,
		`Mention "bot_detection" only when its label is bot_or_generated and its score is 0.8 or more: then add one separate plain sentence`,
		`Never give the score and never call the author a bot.`,
		`never describe the content as a normal or factual news report under detected`,
		`"user_text" must agree with the verdict`,
	} {
		require.Contains(t, flat, w)
	}
}

func TestPropagandaDescriptionMatchesRule(t *testing.T) {
	y := strings.Join(strings.Fields(readRepo(t, "docs/examples/propaganda_detection.yaml")), " ")
	for _, w := range []string{"coarse whole-text classifier", "its score does not measure how strong the propaganda is",
		"PROPAGANDA is supporting evidence only together with another strong signal"} {
		require.Contains(t, y, w)
	}
}

func TestLLMTechniquesPromptDefinition(t *testing.T) {
	p := strings.Join(strings.Fields(readRepo(t, "config/llm_techniques/prompt.md")), " ")
	for _, w := range []string{
		`a contested, causal or sweeping claim is presented as certain ("proves", "definitely", "everyone knows", "there is no doubt", "the real reason is") while the text gives no evidence or only an anecdote`,
		`are NOT a mismatch`,
		`Not flagged: "The ministry said 40 people were killed in the strike on Tuesday."`,
		`Flagged: "This definitely proves the vaccine was designed to harm us."`,
	} {
		require.Contains(t, p, w)
	}
}

func TestExtractorPromptRules(t *testing.T) {
	p := strings.Join(strings.Fields(readRepo(t, "config/extractor/prompt.md")), " ")
	for _, w := range []string{
		`3-8 words`, `names the actor, the assertion and the key number or place`,
		`"Taiwan country" is bad, "China claims Taiwan sovereignty" is good`,
		`One assertion per claim`,
		`"They're given housing preference, organise Land Grabs, bring violent crime and vote ANC." gives four claims: they get housing preference; they organise land grabs; they bring violent crime; they vote ANC`,
		`At most 5 claims are kept: drop the least check-worthy ones.`,
		`rate it 0.6 or more`,
	} {
		require.Contains(t, p, w)
	}
	require.NotContains(t, p, "2-4 words")
}
