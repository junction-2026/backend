package risk

// Every server text of a RiskScore lives here: fixed English templates, filled only with a band id or a technique
// display name. No text may name a model, classifier or algorithm, or contain a digit (texts_test.go).

var bandSummary = map[string]string{
	"low":      "Low risk of manipulation.",
	"moderate": "Moderate risk of manipulation.",
	"high":     "High risk of manipulation.",
	"critical": "Critical risk of manipulation.",
}

// bandSummaryText is the band's meaning; a band id outside the defaults is shown as it is.
func bandSummaryText(band string) string {
	if s, ok := bandSummary[band]; ok {
		return s
	}
	return "Risk of manipulation at level " + band + "."
}

const (
	summaryUnavailable = "No manipulation risk score could be given because the text signals were not available."

	driverTechnique         = "The strongest sign is the use of "
	driverTechniqueNoName   = "The strongest sign is the use of persuasion techniques."
	driverPropagandaPattern = "The overall wording resembles known propaganda writing."
	driverAmplification     = "The author's activity looks automated."
	driverNone              = "No clear signs of manipulation were found."
)

// driverTechniqueText names the technique by its display name.
func driverTechniqueText(display string) string { return driverTechnique + display + "." }

var caveatTexts = map[string]string{
	CaveatContentNotReader:           "This rating describes the content, not whether a reader will be manipulated.",
	CaveatLanguage:                   "The text was not written in English or its language is unknown, so the wording signals are less reliable.",
	CaveatShortText:                  "The text is short, so the rating rests on little evidence.",
	CaveatBotNotIntent:               "The automation signal describes how the account behaves, not what its owner intends.",
	CaveatAmplificationLowConfidence: "The automation signal rests on this single text because the author's posting history is too short.",
	CaveatAmplificationMissing:       "The automation signal is not available, so the rating describes the text alone.",
	CaveatSignalMissing:              "Some text signals were not available, so the rating rests on fewer inputs.",
	CaveatWeakSignal:                 "The propaganda pattern signal is weak, so the rating was lowered.",
}
