package adapter

// techniqueInfo is the built-in label and one-line explanation of a technique.
type techniqueInfo struct{ Display, Explanation string }

// techniqueDisplay maps every name in registry.TechniqueNames.
var techniqueDisplay = map[string]techniqueInfo{
	"Loaded_Language":                    {"Loaded language", "Words chosen to trigger strong emotions"},
	"Appeal_to_fear-prejudice":           {"Appeal to fear or prejudice", "Builds support by stirring fear or prejudice against a group or outcome"},
	"Exaggeration,Minimisation":          {"Exaggeration or minimisation", "Makes something look much bigger or smaller than it is"},
	"Repetition":                         {"Repetition", "Repeats the same message or phrase to make it stick"},
	"Flag-Waving":                        {"Flag-waving", "Appeals to national or group pride to justify a position"},
	"Name_Calling,Labeling":              {"Name calling or labeling", "Attaches a loaded label to a person or group instead of arguing"},
	"Reductio_ad_hitlerum":               {"Reductio ad Hitlerum", "Discredits an idea by comparing it to a hated group or person such as the Nazis"},
	"Black-and-White_Fallacy":            {"Black-and-white fallacy", "Presents only two options when more exist"},
	"Causal_Oversimplification":          {"Causal oversimplification", "Blames a complex outcome on a single cause"},
	"Whataboutism,Straw_Men,Red_Herring": {"Whataboutism, straw man or red herring", "Distracts from the argument by attacking another topic or a distorted version of it"},
	"Straw_Man":                          {"Straw man", "Attacks a distorted version of the opponent's argument"},
	"Red_Herring":                        {"Red herring", "Introduces an irrelevant topic to divert attention"},
	"Doubt":                              {"Doubt", "Questions the credibility of a person, group or institution"},
	"Appeal_to_Authority":                {"Appeal to authority", "Claims something is true because an authority says so"},
	"Thought-terminating_Cliches":        {"Thought-terminating cliché", "Uses a stock phrase to shut down further discussion"},
	"Bandwagon":                          {"Bandwagon", "Urges agreement because everyone else already agrees or acts"},
	"Slogans":                            {"Slogans", "Uses a short, catchy phrase to push a position"},
	"Obfuscation,Intentional_Vagueness,Confusion": {"Obfuscation or intentional vagueness", "Uses unclear wording so the claim can be read in several ways"},
}

// TechniqueDisplay is the built-in English display name of a technique name ("" for an unknown name).
func TechniqueDisplay(name string) string { return techniqueDisplay[name].Display }
