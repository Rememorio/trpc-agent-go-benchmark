//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package experiment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FrozenProtocol defines the independent-seed and split coverage required for
// frozen candidate confirmation. Known regression cases remain visible but are
// not mislabeled as untouched holdout.
type FrozenProtocol struct {
	Name                     string   `json:"name"`
	ValidationScales         []string `json:"validationScales"`
	HoldoutScales            []string `json:"holdoutScales"`
	Repeats                  int      `json:"repeats"`
	MinimumSeedsPerFamily    int      `json:"minimumSeedsPerFamily"`
	KnownRegressionCases     []string `json:"knownRegressionCases"`
	MeaningfulQualityBenefit float64  `json:"meaningfulQualityBenefitPP"`
	MeaningfulTokenReduction float64  `json:"meaningfulTokenReduction"`
}

// DefaultFrozenProtocol is fixed before strict confirmation runs are read.
func DefaultFrozenProtocol() FrozenProtocol {
	return FrozenProtocol{
		Name:                     "skillcraft-frozen-holdout-v1",
		ValidationScales:         []string{"e2", "m2"},
		HoldoutScales:            []string{"e3", "h1"},
		Repeats:                  2,
		MinimumSeedsPerFamily:    2,
		KnownRegressionCases:     []string{"recipe-cookbook-builder/h1"},
		MeaningfulQualityBenefit: 0.5,
		MeaningfulTokenReduction: 0.05,
	}
}

// FrozenMetrics pools evaluator objectives from paired candidate comparisons.
type FrozenMetrics struct {
	Pairs            int     `json:"pairs"`
	PassRate         float64 `json:"passRate"`
	AverageQuality   float64 `json:"averageQuality"`
	AverageScore     float64 `json:"averageScore"`
	AverageTokens    float64 `json:"averageAgentTokens"`
	AverageToolCalls float64 `json:"averageToolCalls"`
	AverageDuration  float64 `json:"averageDurationSeconds"`
}

// FrozenDelta describes candidate-minus-seed changes for the objectives that
// are available in an optimizer comparison.
type FrozenDelta struct {
	PassRatePP    float64 `json:"passRatePP"`
	QualityPP     float64 `json:"qualityPP"`
	Score         float64 `json:"averageScore"`
	AgentTokens   float64 `json:"averageAgentTokens"`
	AgentTokensPC float64 `json:"agentTokensPercent"`
	ToolCalls     float64 `json:"averageToolCalls"`
	DurationSec   float64 `json:"averageDurationSeconds"`
}

// FrozenComparison is one pooled seed-versus-candidate view.
type FrozenComparison struct {
	Seed      FrozenMetrics `json:"seed"`
	Candidate FrozenMetrics `json:"candidate"`
	Delta     FrozenDelta   `json:"delta"`
	Outcomes  PairSummary   `json:"outcomes"`
}

// FrozenSource identifies one canonical fixed-comparison result.
type FrozenSource struct {
	Name          string `json:"name"`
	Family        string `json:"family"`
	RandomSeed    int64  `json:"randomSeed"`
	CandidateHash string `json:"candidateHash"`
	Promoted      bool   `json:"promoted"`
}

// FrozenEvidence is the machine-readable strict confirmation verdict.
type FrozenEvidence struct {
	Protocol          FrozenProtocol              `json:"protocol"`
	Sources           []FrozenSource              `json:"sources"`
	Validation        FrozenComparison            `json:"validation"`
	Holdout           FrozenComparison            `json:"holdout"`
	Untouched         FrozenComparison            `json:"untouchedHoldout"`
	KnownRegression   FrozenComparison            `json:"knownRegressionHoldout"`
	Families          map[string]FrozenComparison `json:"families"`
	Gates             []GateCheck                 `json:"gates"`
	PromotionEligible bool                        `json:"promotionEligible"`
	PromotionReason   string                      `json:"promotionReason"`
}

type frozenInput struct {
	Optimization *struct {
		RandomSeed       int64    `json:"randomSeed"`
		Repeats          int      `json:"repeats"`
		ValidationScales []string `json:"validationScales"`
		HoldoutScales    []string `json:"holdoutScales"`
		SelectedChanged  bool     `json:"selectedChanged"`
		Search           *struct {
			Spec              json.RawMessage `json:"spec"`
			PromotionEligible bool            `json:"promotion_eligible"`
			PromotionReason   string          `json:"promotion_reason"`
		} `json:"search"`
		Comparison *struct {
			Validation []frozenPair `json:"validation"`
			Holdout    []frozenPair `json:"holdout"`
		} `json:"comparison"`
	} `json:"optimization"`
}

type frozenPair struct {
	CaseID    string           `json:"caseId"`
	Seed      int64            `json:"seed"`
	Baseline  frozenEvaluation `json:"baseline"`
	Candidate frozenEvaluation `json:"candidate"`
}

type frozenEvaluation struct {
	Score      float64            `json:"score"`
	Objectives map[string]float64 `json:"objectives"`
}

type sourcedFrozenPair struct {
	family    string
	scale     string
	baseline  frozenEvaluation
	candidate frozenEvaluation
}

// AggregateFrozen validates independent fixed-comparison results and separates
// untouched holdout from cases already used during benchmark development.
func AggregateFrozen(paths []string, protocol FrozenProtocol) (*FrozenEvidence, error) {
	if len(paths) == 0 {
		return nil, errors.New("experiment: no frozen result files")
	}
	if protocol.Repeats <= 0 || protocol.MinimumSeedsPerFamily <= 0 ||
		len(protocol.ValidationScales) == 0 || len(protocol.HoldoutScales) == 0 {
		return nil, errors.New("experiment: incomplete frozen protocol")
	}
	known := make(map[string]bool, len(protocol.KnownRegressionCases))
	for _, item := range protocol.KnownRegressionCases {
		known[item] = true
	}
	var validation, holdout, untouched, regressions []sourcedFrozenPair
	byFamily := make(map[string][]sourcedFrozenPair)
	seedsByFamily := make(map[string]map[int64]bool)
	candidateByFamily := make(map[string]string)
	sources := make([]FrozenSource, 0, len(paths))
	allPromoted := true
	for _, path := range paths {
		input, err := loadFrozenInput(path)
		if err != nil {
			return nil, err
		}
		family, hash, err := validateFrozenInput(input, protocol)
		if err != nil {
			return nil, fmt.Errorf("experiment: %s: %w", path, err)
		}
		if existing := candidateByFamily[family]; existing != "" && existing != hash {
			return nil, fmt.Errorf("experiment: family %s used multiple frozen candidates", family)
		}
		candidateByFamily[family] = hash
		if seedsByFamily[family] == nil {
			seedsByFamily[family] = make(map[int64]bool)
		}
		if seedsByFamily[family][input.Optimization.RandomSeed] {
			return nil, fmt.Errorf("experiment: duplicate frozen seed %d for %s", input.Optimization.RandomSeed, family)
		}
		seedsByFamily[family][input.Optimization.RandomSeed] = true
		promoted := input.Optimization.Search.PromotionEligible
		allPromoted = allPromoted && promoted
		sources = append(sources, FrozenSource{
			Name:          filepath.Base(filepath.Dir(path)),
			Family:        family,
			RandomSeed:    input.Optimization.RandomSeed,
			CandidateHash: hash,
			Promoted:      promoted,
		})
		validation = append(validation, sourceFrozenPairs(input.Optimization.Comparison.Validation)...)
		for _, pair := range sourceFrozenPairs(input.Optimization.Comparison.Holdout) {
			holdout = append(holdout, pair)
			byFamily[pair.family] = append(byFamily[pair.family], pair)
			if known[pair.family+"/"+pair.scale] {
				regressions = append(regressions, pair)
			} else {
				untouched = append(untouched, pair)
			}
		}
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Family == sources[j].Family {
			return sources[i].RandomSeed < sources[j].RandomSeed
		}
		return sources[i].Family < sources[j].Family
	})
	for family, seeds := range seedsByFamily {
		if len(seeds) < protocol.MinimumSeedsPerFamily {
			return nil, fmt.Errorf("experiment: family %s has %d seed(s); need %d", family, len(seeds), protocol.MinimumSeedsPerFamily)
		}
	}
	evidence := &FrozenEvidence{
		Protocol:        protocol,
		Sources:         sources,
		Validation:      summarizeFrozen(validation),
		Holdout:         summarizeFrozen(holdout),
		Untouched:       summarizeFrozen(untouched),
		KnownRegression: summarizeFrozen(regressions),
		Families:        make(map[string]FrozenComparison, len(byFamily)),
	}
	for family, pairs := range byFamily {
		evidence.Families[family] = summarizeFrozen(pairs)
	}
	evidence.Gates = frozenGates(evidence, allPromoted)
	evidence.PromotionEligible = true
	for _, gate := range evidence.Gates {
		if !gate.Passed {
			evidence.PromotionEligible = false
			break
		}
	}
	if evidence.PromotionEligible {
		evidence.PromotionReason = "all frozen validation, holdout, and independent-seed gates passed"
	} else {
		evidence.PromotionReason = "one or more frozen confirmation gates failed"
	}
	return evidence, nil
}

func loadFrozenInput(path string) (*frozenInput, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("experiment: read %s: %w", path, err)
	}
	var input frozenInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("experiment: decode %s: %w", path, err)
	}
	return &input, nil
}

func validateFrozenInput(input *frozenInput, p FrozenProtocol) (string, string, error) {
	if input == nil || input.Optimization == nil || input.Optimization.Search == nil ||
		input.Optimization.Comparison == nil {
		return "", "", errors.New("requires a canonical frozen optimization comparison")
	}
	opt := input.Optimization
	if !opt.SelectedChanged {
		return "", "", errors.New("frozen candidate is identical to seed")
	}
	if opt.Repeats != p.Repeats || !sameStrings(opt.ValidationScales, p.ValidationScales) ||
		!sameStrings(opt.HoldoutScales, p.HoldoutScales) {
		return "", "", errors.New("split or repeat configuration does not match protocol")
	}
	family, err := validateFrozenPairs(opt.Comparison.Validation, "validation", p.ValidationScales, p.Repeats)
	if err != nil {
		return "", "", err
	}
	holdoutFamily, err := validateFrozenPairs(opt.Comparison.Holdout, "holdout", p.HoldoutScales, p.Repeats)
	if err != nil {
		return "", "", err
	}
	if family != holdoutFamily {
		return "", "", errors.New("validation and holdout families differ")
	}
	digest := sha256.Sum256(opt.Search.Spec)
	return family, hex.EncodeToString(digest[:8]), nil
}

func validateFrozenPairs(pairs []frozenPair, split string, scales []string, repeats int) (string, error) {
	if len(pairs) != len(scales)*repeats {
		return "", fmt.Errorf("%s has %d pairs; expected %d", split, len(pairs), len(scales)*repeats)
	}
	expected := make(map[string]bool, len(pairs))
	for _, scale := range scales {
		for repeat := 1; repeat <= repeats; repeat++ {
			expected[fmt.Sprintf("%s/r%d", scale, repeat)] = true
		}
	}
	var family string
	for _, pair := range pairs {
		parts := strings.Split(pair.CaseID, "/")
		if len(parts) != 4 || parts[0] != split {
			return "", fmt.Errorf("invalid %s case id %q", split, pair.CaseID)
		}
		if family == "" {
			family = parts[1]
		} else if family != parts[1] {
			return "", fmt.Errorf("%s mixes task families", split)
		}
		key := parts[2] + "/" + parts[3]
		if !expected[key] {
			return "", fmt.Errorf("unexpected or duplicate %s case %q", split, pair.CaseID)
		}
		if err := validateFrozenEvaluation(pair.Baseline); err != nil {
			return "", fmt.Errorf("%s baseline %q: %w", split, pair.CaseID, err)
		}
		if err := validateFrozenEvaluation(pair.Candidate); err != nil {
			return "", fmt.Errorf("%s candidate %q: %w", split, pair.CaseID, err)
		}
		delete(expected, key)
	}
	return family, nil
}

func validateFrozenEvaluation(evaluation frozenEvaluation) error {
	for _, objective := range []string{
		"passed", "official_quality", "agent_tokens", "tool_calls", "duration_seconds",
	} {
		if _, ok := evaluation.Objectives[objective]; !ok {
			return fmt.Errorf("missing %q objective", objective)
		}
	}
	return nil
}

func sourceFrozenPairs(pairs []frozenPair) []sourcedFrozenPair {
	result := make([]sourcedFrozenPair, 0, len(pairs))
	for _, pair := range pairs {
		parts := strings.Split(pair.CaseID, "/")
		result = append(result, sourcedFrozenPair{
			family: parts[1], scale: parts[2],
			baseline: pair.Baseline, candidate: pair.Candidate,
		})
	}
	return result
}

func summarizeFrozen(pairs []sourcedFrozenPair) FrozenComparison {
	result := FrozenComparison{Outcomes: PairSummary{Pairs: len(pairs)}}
	result.Seed = frozenMetrics(pairs, false)
	result.Candidate = frozenMetrics(pairs, true)
	result.Delta = FrozenDelta{
		PassRatePP:  round(result.Candidate.PassRate - result.Seed.PassRate),
		QualityPP:   round(result.Candidate.AverageQuality - result.Seed.AverageQuality),
		Score:       round(result.Candidate.AverageScore - result.Seed.AverageScore),
		AgentTokens: round(result.Candidate.AverageTokens - result.Seed.AverageTokens),
		ToolCalls:   round(result.Candidate.AverageToolCalls - result.Seed.AverageToolCalls),
		DurationSec: round(result.Candidate.AverageDuration - result.Seed.AverageDuration),
	}
	if result.Seed.AverageTokens != 0 {
		result.Delta.AgentTokensPC = round(result.Delta.AgentTokens / result.Seed.AverageTokens * 100)
	}
	for _, pair := range pairs {
		basePassed := pair.baseline.Objectives["passed"] >= 0.5
		candidatePassed := pair.candidate.Objectives["passed"] >= 0.5
		switch {
		case candidatePassed && !basePassed:
			result.Outcomes.PassWins++
		case !candidatePassed && basePassed:
			result.Outcomes.PassLosses++
		default:
			result.Outcomes.PassTies++
		}
		delta := pair.candidate.Objectives["official_quality"] - pair.baseline.Objectives["official_quality"]
		switch {
		case delta > 1e-9:
			result.Outcomes.QualityWins++
		case delta < -1e-9:
			result.Outcomes.QualityLosses++
		default:
			result.Outcomes.QualityTies++
		}
	}
	return result
}

func frozenMetrics(pairs []sourcedFrozenPair, candidate bool) FrozenMetrics {
	metrics := FrozenMetrics{Pairs: len(pairs)}
	if len(pairs) == 0 {
		return metrics
	}
	var passed, quality, score, tokens, tools, duration float64
	for _, pair := range pairs {
		evaluation := pair.baseline
		if candidate {
			evaluation = pair.candidate
		}
		passed += evaluation.Objectives["passed"]
		quality += evaluation.Objectives["official_quality"] * 100
		score += evaluation.Score
		tokens += evaluation.Objectives["agent_tokens"]
		tools += evaluation.Objectives["tool_calls"]
		duration += evaluation.Objectives["duration_seconds"]
	}
	count := float64(len(pairs))
	metrics.PassRate = round(passed / count * 100)
	metrics.AverageQuality = round(quality / count)
	metrics.AverageScore = round(score / count)
	metrics.AverageTokens = round(tokens / count)
	metrics.AverageToolCalls = round(tools / count)
	metrics.AverageDuration = round(duration / count)
	return metrics
}

func frozenGates(e *FrozenEvidence, allPromoted bool) []GateCheck {
	passNonRegression := e.Holdout.Candidate.PassRate >= e.Holdout.Seed.PassRate
	qualityNonRegression := e.Holdout.Candidate.AverageQuality >= e.Holdout.Seed.AverageQuality
	untouchedNonRegression := e.Untouched.Candidate.PassRate >= e.Untouched.Seed.PassRate &&
		e.Untouched.Candidate.AverageQuality >= e.Untouched.Seed.AverageQuality
	qualityBenefit := e.Holdout.Delta.QualityPP >= e.Protocol.MeaningfulQualityBenefit
	tokenBenefit := e.Holdout.Delta.AgentTokensPC <= -e.Protocol.MeaningfulTokenReduction*100
	return []GateCheck{
		{Name: "per-run-promotion", Passed: allPromoted, Details: "every canonical fixed comparison must pass its validation and holdout gate"},
		{Name: "holdout-pass-non-regression", Passed: passNonRegression, Details: fmt.Sprintf("candidate %.2f%% vs seed %.2f%%", e.Holdout.Candidate.PassRate, e.Holdout.Seed.PassRate)},
		{Name: "holdout-quality-non-regression", Passed: qualityNonRegression, Details: fmt.Sprintf("candidate %.2f vs seed %.2f", e.Holdout.Candidate.AverageQuality, e.Holdout.Seed.AverageQuality)},
		{Name: "untouched-non-regression", Passed: untouchedNonRegression, Details: fmt.Sprintf("%d untouched pair(s)", e.Untouched.Seed.Pairs)},
		{Name: "meaningful-benefit", Passed: qualityBenefit || tokenBenefit, Details: fmt.Sprintf("quality delta %.2fpp or token delta %.2f%%", e.Holdout.Delta.QualityPP, e.Holdout.Delta.AgentTokensPC)},
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
