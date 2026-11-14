package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const englishParagraph = `The city has been talking about the new transport plan for several weeks, and many people are worried that it will not ` +
	`solve the problems they face every day. Some say the buses are too slow, others say there are not enough of them, and a few believe ` +
	`that the money would be better spent on the roads. We do not know yet what the council will decide, but we hope that it will listen ` +
	`to the people who live here and use the service before it makes its final choice on the plan.`

func TestDetectEnglish(t *testing.T) {
	for _, c := range []struct {
		name, text string
		want       englishVerdict
	}{
		{"english propaganda", "They are LYING to you! Only we tell the truth. Wake up before it's too late, patriots!", verdictEnglish},
		{"english curly apostrophe", "They are LYING to you! Only we tell the truth. Wake up before it’s too late, patriots!", verdictEnglish},
		{"english news", "The city council meets on Tuesday at 6pm to discuss the new bike lanes.", verdictEnglish},
		{"english paragraph", englishParagraph, verdictEnglish},
		{"english with url and tags", "Read this: https://example.com/a?b=c www.x.org @bob #wow They are LYING to you! Only we tell the truth, and it is the end of it.", verdictEnglish},
		{"german", "Die Regierung belügt euch alle! Nur wir sagen die Wahrheit, wacht endlich auf, bevor es zu spät ist!", verdictNotEnglish},
		{"french", "Le gouvernement vous ment à tous, nous sommes les seuls à dire la vérité et il est temps de vous réveiller.", verdictNotEnglish},
		{"spanish", "El gobierno os miente a todos, somos los únicos que decimos la verdad y es hora de que despertéis de una vez.", verdictNotEnglish},
		{"russian", "Правительство лжёт вам всем, только мы говорим правду, просыпайтесь, пока не стало слишком поздно!", verdictNotEnglish},
		{"ukrainian", "Влада бреше вам усім, тільки ми говоримо правду, прокидайтеся, поки не стало надто пізно, патріоти!", verdictNotEnglish},
		{"greek", "Η κυβέρνηση σας λέει ψέματα όλους, μόνο εμείς λέμε την αλήθεια, ξυπνήστε πριν να είναι πολύ αργά, πατριώτες!", verdictNotEnglish},
		{"one word and emoji", "BREAKING!!! 🚨 #EU", verdictUnsure},
		{"german with english quote", `Der Minister sagte gestern: "We will not stop until this is all over", und das ist eine Schande für uns.`, verdictUnsure},
		{"names only", "OK, deal, Brexit Boris Johnson", verdictUnsure},
		{"url only", "https://example.com/some/long/path?x=1 www.example.org", verdictUnsure},
		{"short russian is unsure", "Привіт, світе", verdictUnsure},
		{"empty", "", verdictUnsure},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, detectEnglish(c.text, 8, 0.25))
		})
	}
}

func TestTextWords(t *testing.T) {
	require.Equal(t, []string{"don't", "it’s", "6pm"[1:], "ok"}, textWords("Don't it’s 6pm -- OK 123 🚨 ' @bob #tag https://x.y/z www.a.b"))
}
