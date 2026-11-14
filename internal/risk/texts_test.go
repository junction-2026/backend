package risk

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
)

func TestTextsHaveNoDigitsOrModelNames(t *testing.T) {
	var all []string
	for _, b := range bandSummary {
		all = append(all, b)
	}
	all = append(all, summaryUnavailable, driverTechniqueNoName, driverPropagandaPattern, driverAmplification, driverNone, driverTechniqueText(adapter.TechniqueDisplay("Doubt")))
	for _, c := range caveatTexts {
		all = append(all, c)
	}
	require.Len(t, caveatTexts, 8)
	digit := regexp.MustCompile(`[0-9]`)
	for _, s := range all {
		require.NotEmpty(t, s)
		require.False(t, digit.MatchString(s), s)
		for _, w := range []string{"nci-", "classifier", "model"} {
			require.NotContains(t, strings.ToLower(s), w, s)
		}
	}
	for _, n := range []string{"Appeal_to_fear-prejudice", "Obfuscation,Intentional_Vagueness,Confusion", "Doubt"} {
		require.False(t, digit.MatchString(adapter.TechniqueDisplay(n)))
	}
}

func TestRenderedScoreTexts(t *testing.T) {
	j := withAuthor(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8), bot(0.9, 120, 0.6)))
	s := Compute(j, botMapped())
	texts := []string{s.Summary}
	for _, d := range s.Drivers {
		texts = append(texts, d.Text)
	}
	for _, c := range s.Caveats {
		texts = append(texts, c.Text)
	}
	for _, x := range texts {
		require.NotRegexp(t, `[0-9]`, x)
		require.NotContains(t, strings.ToLower(x), "model")
	}
}
