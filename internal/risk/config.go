// Package risk computes the Manipulation Risk Score (shared capability manipulation-risk-score): a pure function of the
// settled job and the risk configuration.
package risk

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"gopkg.in/yaml.v3"
)

// Weights2 and Weights3 are the dimension weights of a RiskConfig.
type Weights2 struct {
	Intensity    float64 `json:"intensity" yaml:"intensity"`
	Confirmation float64 `json:"confirmation" yaml:"confirmation"`
}

type Weights3 struct {
	Intensity     float64 `json:"intensity" yaml:"intensity"`
	Confirmation  float64 `json:"confirmation" yaml:"confirmation"`
	Amplification float64 `json:"amplification" yaml:"amplification"`
}

type ImpactWeights struct {
	High   float64 `json:"high" yaml:"high"`
	Medium float64 `json:"medium" yaml:"medium"`
	Lower  float64 `json:"lower" yaml:"lower"`
}

type Band struct {
	Band string  `json:"band" yaml:"band"`
	Min  float64 `json:"min" yaml:"min"`
}

// Config is the shared RiskConfig object; a RiskScore echoes it.
type Config struct {
	DetectionThreshold       float64       `json:"detection_threshold" yaml:"detection_threshold"`
	ReferenceTechniqueCount  float64       `json:"reference_technique_count" yaml:"reference_technique_count"`
	WeightedSumReference     float64       `json:"weighted_sum_reference" yaml:"weighted_sum_reference"`
	ImpactWeights            ImpactWeights `json:"impact_weights" yaml:"impact_weights"`
	DampeningThreshold       float64       `json:"dampening_threshold" yaml:"dampening_threshold"`
	DampeningFactor          float64       `json:"dampening_factor" yaml:"dampening_factor"`
	MinAuthorPosts           int           `json:"min_author_posts" yaml:"min_author_posts"`
	ShortTextMaxWords        int           `json:"short_text_max_words" yaml:"short_text_max_words"`
	DriverMinContribution    float64       `json:"driver_min_contribution" yaml:"driver_min_contribution"`
	WeightsWithAmplification Weights3      `json:"weights_with_amplification" yaml:"weights_with_amplification"`
	WeightsWithoutAmp        Weights2      `json:"weights_without_amplification" yaml:"weights_without_amplification"`
	Bands                    []Band        `json:"bands" yaml:"bands"`
}

// BotMapping names, as dot-separated paths into the bot_detection result, the members the amplification dimension reads.
// A numeric segment indexes an array. It is deployment configuration and is not echoed in a RiskScore.
type BotMapping struct {
	AuthorScore     string `yaml:"author_score"`
	AuthorPosts     string `yaml:"author_posts"`
	SingleTextScore string `yaml:"single_text_score"`
}

// Settings is the whole risk configuration.
type Settings struct {
	Config     `yaml:",inline"`
	BotMapping BotMapping `yaml:"bot_detection"`
}

// Default returns the shared defaults.
func Default() Settings {
	return Settings{Config: Config{
		DetectionThreshold:       0.3,
		ReferenceTechniqueCount:  6,
		WeightedSumReference:     5.0,
		ImpactWeights:            ImpactWeights{High: 1.0, Medium: 0.7, Lower: 0.4},
		DampeningThreshold:       0.2,
		DampeningFactor:          0.6,
		MinAuthorPosts:           20,
		ShortTextMaxWords:        80,
		DriverMinContribution:    0.05,
		WeightsWithAmplification: Weights3{Intensity: 0.4, Confirmation: 0.3, Amplification: 0.3},
		WeightsWithoutAmp:        Weights2{Intensity: 0.55, Confirmation: 0.45},
		Bands:                    []Band{{"low", 0}, {"moderate", 0.25}, {"high", 0.5}, {"critical", 0.75}},
	}}
}

// Load reads the YAML file at path over the defaults (path "": the defaults) and validates it.
func Load(path string) (Settings, error) {
	s := Default()
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("RISK_SCORE_CONFIG cannot be read: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return Default(), fmt.Errorf("RISK_SCORE_CONFIG %s: %w", path, err)
	}
	if err := s.Validate(); err != nil {
		return Default(), fmt.Errorf("RISK_SCORE_CONFIG %s: %w", path, err)
	}
	return s, nil
}

// Validate reports the first invalid member.
func (c Config) Validate() error {
	unit := func(name string, v float64) error {
		if !(v >= 0 && v <= 1) { // also rejects NaN
			return fmt.Errorf("%s must be between 0 and 1", name)
		}
		return nil
	}
	pos := func(name string, v float64) error {
		if !(v > 0) || math.IsInf(v, 0) {
			return fmt.Errorf("%s must be greater than 0", name)
		}
		return nil
	}
	for _, e := range []error{
		unit("detection_threshold", c.DetectionThreshold),
		unit("dampening_threshold", c.DampeningThreshold),
		unit("dampening_factor", c.DampeningFactor),
		unit("driver_min_contribution", c.DriverMinContribution),
		pos("reference_technique_count", c.ReferenceTechniqueCount),
		pos("weighted_sum_reference", c.WeightedSumReference),
		pos("min_author_posts", float64(c.MinAuthorPosts)),
		pos("short_text_max_words", float64(c.ShortTextMaxWords)),
		nonNeg("impact_weights.high", c.ImpactWeights.High),
		nonNeg("impact_weights.medium", c.ImpactWeights.Medium),
		nonNeg("impact_weights.lower", c.ImpactWeights.Lower),
		weights("weights_with_amplification", c.WeightsWithAmplification.Intensity, c.WeightsWithAmplification.Confirmation, c.WeightsWithAmplification.Amplification),
		weights("weights_without_amplification", c.WeightsWithoutAmp.Intensity, c.WeightsWithoutAmp.Confirmation, 0),
		c.validateBands(),
	} {
		if e != nil {
			return e
		}
	}
	return nil
}

func nonNeg(name string, v float64) error {
	if !(v >= 0) || math.IsInf(v, 0) {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}

// weights checks one weight set: no negative weight, intensity and confirmation positive, sum 1 (scores stay within 0..1).
func weights(name string, intensity, confirmation, amplification float64) error {
	for _, w := range []float64{intensity, confirmation, amplification} {
		if err := nonNeg(name, w); err != nil {
			return err
		}
	}
	if intensity <= 0 || confirmation <= 0 {
		return fmt.Errorf("%s: intensity and confirmation must be greater than 0", name)
	}
	if math.Abs(intensity+confirmation+amplification-1) > 1e-9 {
		return fmt.Errorf("%s must sum to 1", name)
	}
	return nil
}

func (c Config) validateBands() error {
	if len(c.Bands) == 0 {
		return errors.New("bands must not be empty")
	}
	if c.Bands[0].Min != 0 {
		return errors.New("bands must start with min 0")
	}
	for i, b := range c.Bands {
		if b.Band == "" {
			return fmt.Errorf("bands[%d].band must not be empty", i)
		}
		if !(b.Min >= 0 && b.Min <= 1) {
			return fmt.Errorf("bands[%d].min must be between 0 and 1", i)
		}
		if i > 0 && !(b.Min > c.Bands[i-1].Min) {
			return errors.New("bands must be in ascending min order")
		}
	}
	return nil
}
