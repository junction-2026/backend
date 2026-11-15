package country

import "testing"

func TestLoad(t *testing.T) {
	tb, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if tb.Len() != 249 {
		t.Fatalf("got %d codes, want the 249 officially assigned ones", tb.Len())
	}
	for in, want := range map[string]string{"us": "United States", " DE ": "Germany", "Ru": "Russia", "UA": "Ukraine"} {
		if _, n, ok := tb.Name(in); !ok || n != want {
			t.Errorf("Name(%q) = %q, %v", in, n, ok)
		}
	}
	for _, bad := range []string{"EU", "UK", "XX", "XK", "USA", "", "U"} {
		if _, _, ok := tb.Name(bad); ok {
			t.Errorf("Name(%q) accepted", bad)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, b := range []string{"", "code,name\n", "id,name\nDE,Germany\n", "code,name\nde,Germany\n", "code,name\nDE,\n",
		"code,name\nDE,Germany\nDE,Germany\n", "code,name\nDEU,Germany\n", "code,name\nDE\n"} {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("Parse(%q) succeeded", b)
		}
	}
}
