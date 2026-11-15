package adapter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinalizerLanguage(t *testing.T) {
	for in, want := range map[string]string{"": "en (English)", "en": "en (English)", "de": "de (German)", "fi": "fi (Finnish)", "xx": "en (English)"} {
		c, n := FinalizerLanguage(in)
		require.Equal(t, want, c+" ("+n+")", in)
	}
}

func TestDetectLanguage(t *testing.T) {
	for want, text := range map[string]string{
		"de": "Dieser Text ist nicht neutral, weil er den Lesern Angst macht und die Fakten nicht zeigt.",
		"fr": "Ce texte n'est pas neutre, parce qu'il fait peur aux lecteurs et ne montre pas les faits dans le contexte.",
		"es": "Este texto no es neutral, porque asusta a los lectores y no muestra los hechos con el contexto que es necesario.",
		"nl": "Deze tekst is niet neutraal, omdat het de lezers bang maakt en de feiten niet met de nodige zorg laat zien.",
		"en": "This text is not neutral, because it scares the readers and does not show the facts with the care that is needed.",
		"":   "Das ist kurz.",
	} {
		require.Equal(t, want, detectLanguage(text, 8, 0.25), text)
	}
	require.Equal(t, "", detectLanguage("Этот текст не нейтрален, потому что он пугает читателей и не показывает факты с нужной осторожностью.", 8, 0.25))
}

func TestWrongLanguage(t *testing.T) {
	de := json.RawMessage(`{"user_text":"Dieser Text ist nicht neutral, weil er den Lesern Angst macht und die Fakten nicht zeigt."}`)
	require.True(t, WrongLanguage("en", de))
	require.False(t, WrongLanguage("de", de))
	require.False(t, WrongLanguage("fi", de), "expected language unknown to the detector")
	require.False(t, WrongLanguage("en", json.RawMessage(`{"user_text":3}`)))
}
