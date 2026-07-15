//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package experiment aggregates reproducible multi-seed SkillCraft compare
// runs and applies the promotion protocol fixed before the runs are observed.
package experiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	armBaseline  = "baseline"
	armEvolution = "evolution"
	armOptimized = "optimized_evolution"
)

// Protocol defines the coverage and non-regression gates for an operational
// three-arm replay. Thresholds use percentage points unless stated otherwise.
type Protocol struct {
	Name                       string   `json:"name"`
	ExpectedFamilies           []string `json:"expectedFamilies"`
	ExpectedScales             []string `json:"expectedScales"`
	MinimumRuns                int      `json:"minimumRuns"`
	OverallQualityTolerancePP  float64  `json:"overallQualityTolerancePP"`
	FamilyQualityTolerancePP   float64  `json:"familyQualityTolerancePP"`
	MeaningfulQualityBenefitPP float64  `json:"meaningfulQualityBenefitPP"`
	MeaningfulTokenReduction   float64  `json:"meaningfulTokenReduction"`
}

// DefaultProtocol is the preregistered protocol used by the reflective
// optimizer experiment in this repository.
func DefaultProtocol() Protocol {
	return Protocol{
		Name: "skillcraft-5-family-3-arm-v1",
		ExpectedFamilies: []string{
			"cat-facts-collector",
			"openmeteo-weather",
			"pokeapi-pokedex",
			"recipe-cookbook-builder",
			"world-bank-economic-snapshot",
		},
		ExpectedScales: []string{
			"e1", "e2", "e3", "m1", "m2", "h1",
		},
		MinimumRuns:                3,
		OverallQualityTolerancePP:  0.25,
		FamilyQualityTolerancePP:   1.0,
		MeaningfulQualityBenefitPP: 0.5,
		MeaningfulTokenReduction:   0.05,
	}
}

// Metrics is a pooled arm summary. Tokens and duration are per task.
type Metrics struct {
	Tasks                 int     `json:"tasks"`
	PassedTasks           int     `json:"passedTasks"`
	PassRate              float64 `json:"passRate"`
	AverageQuality        float64 `json:"averageQuality"`
	AverageAgentTokens    float64 `json:"averageAgentTokens"`
	AverageReviewerTokens float64 `json:"averageReviewerTokens"`
	AverageEndToEndTokens float64 `json:"averageEndToEndTokens"`
	AverageToolCalls      float64 `json:"averageToolCalls"`
	AverageDurationSec    float64 `json:"averageDurationSeconds"`
}

// Delta contains candidate minus control metrics.
type Delta struct {
	PassRatePP       float64 `json:"passRatePP"`
	QualityPP        float64 `json:"qualityPP"`
	AgentTokens      float64 `json:"agentTokens"`
	ReviewerTokens   float64 `json:"reviewerTokens"`
	EndToEndTokens   float64 `json:"endToEndTokens"`
	EndToEndTokensPC float64 `json:"endToEndTokensPercent"`
	ToolCalls        float64 `json:"toolCalls"`
	DurationSec      float64 `json:"durationSeconds"`
}

// PairSummary counts paired outcomes for optimized evolution versus evolution.
type PairSummary struct {
	Pairs         int `json:"pairs"`
	PassWins      int `json:"passWins"`
	PassTies      int `json:"passTies"`
	PassLosses    int `json:"passLosses"`
	QualityWins   int `json:"qualityWins"`
	QualityTies   int `json:"qualityTies"`
	QualityLosses int `json:"qualityLosses"`
}

// Comparison describes one named candidate-versus-control comparison.
type Comparison struct {
	Control   string      `json:"control"`
	Candidate string      `json:"candidate"`
	Delta     Delta       `json:"delta"`
	Pairs     PairSummary `json:"pairs"`
}

// GateCheck is one independently auditable promotion condition.
type GateCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// RunSummary keeps one input run auditable without retaining machine-specific
// paths or task transcripts.
type RunSummary struct {
	Name                          string             `json:"name"`
	RootSeed                      int64              `json:"rootSeed"`
	RunOrder                      []string           `json:"runOrder"`
	Arms                          map[string]Metrics `json:"arms"`
	EvolutionVsBaseline           Comparison         `json:"evolutionVsBaseline"`
	OptimizedEvolutionVsEvolution Comparison         `json:"optimizedEvolutionVsEvolution"`
}

// Coverage records the realized matrix.
type Coverage struct {
	Runs          int      `json:"runs"`
	TasksPerArm   int      `json:"tasksPerArm"`
	TotalArmCases int      `json:"totalArmCases"`
	Families      []string `json:"families"`
	Scales        []string `json:"scales"`
}

// Evidence is the machine-readable aggregate and gate verdict.
type Evidence struct {
	Protocol                      Protocol                      `json:"protocol"`
	Runs                          []RunSummary                  `json:"runs"`
	Coverage                      Coverage                      `json:"coverage"`
	Arms                          map[string]Metrics            `json:"arms"`
	Families                      map[string]map[string]Metrics `json:"families"`
	EvolutionVsBaseline           Comparison                    `json:"evolutionVsBaseline"`
	OptimizedEvolutionVsEvolution Comparison                    `json:"optimizedEvolutionVsEvolution"`
	Gates                         []GateCheck                   `json:"gates"`
	PromotionEligible             bool                          `json:"promotionEligible"`
	PromotionReason               string                        `json:"promotionReason"`
}

type inputResult struct {
	EvaluationSeed     *int64    `json:"evaluationSeed"`
	RunOrder           []string  `json:"runOrder"`
	Baseline           *inputArm `json:"baseline"`
	Evolution          *inputArm `json:"evolution"`
	OptimizedEvolution *inputArm `json:"optimizedEvolution"`
}

type inputArm struct {
	Cases []inputCase `json:"cases"`
}

type inputCase struct {
	TaskID              string     `json:"taskId"`
	BaseTask            string     `json:"baseTask"`
	Scale               string     `json:"scale"`
	EvaluationSeed      *int64     `json:"evaluationSeed"`
	DurationSeconds     float64    `json:"durationSeconds"`
	TotalTokens         int        `json:"totalTokens"`
	ReviewerTotalTokens int        `json:"reviewerTotalTokens"`
	EndToEndTotalTokens int        `json:"endToEndTotalTokens"`
	ToolCalls           []string   `json:"toolCalls"`
	Evaluation          *inputEval `json:"evaluation"`
}

type inputEval struct {
	Passed bool `json:"passed"`
	Score  struct {
		Percent float64 `json:"percent"`
	} `json:"score"`
}

type collectedCase struct {
	arm  string
	run  int
	data inputCase
}

// Aggregate validates and aggregates result files in one operation.
func Aggregate(paths []string, protocol Protocol) (*Evidence, error) {
	if err := validateProtocol(protocol); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("experiment: no input result files")
	}
	runs := make([]RunSummary, 0, len(paths))
	seenSeeds := make(map[int64]struct{}, len(paths))
	all := make(map[string][]collectedCase, 3)
	for idx, path := range paths {
		input, err := loadInput(path)
		if err != nil {
			return nil, err
		}
		if input.EvaluationSeed == nil {
			return nil, fmt.Errorf("experiment: %s has no evaluationSeed", path)
		}
		if _, exists := seenSeeds[*input.EvaluationSeed]; exists {
			return nil, fmt.Errorf("experiment: duplicate root seed %d", *input.EvaluationSeed)
		}
		seenSeeds[*input.EvaluationSeed] = struct{}{}
		if err := validateInput(input, protocol); err != nil {
			return nil, fmt.Errorf("experiment: %s: %w", path, err)
		}
		runCases := make(map[string][]collectedCase, 3)
		for arm, cases := range inputArms(input) {
			for _, item := range cases {
				collected := collectedCase{arm: arm, run: idx, data: item}
				all[arm] = append(all[arm], collected)
				runCases[arm] = append(runCases[arm], collected)
			}
		}
		runs = append(runs, summarizeRun(path, input, runCases))
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].RootSeed < runs[j].RootSeed })

	evidence := &Evidence{
		Protocol: protocol,
		Runs:     runs,
		Coverage: Coverage{
			Runs:          len(runs),
			TasksPerArm:   len(protocol.ExpectedFamilies) * len(protocol.ExpectedScales),
			TotalArmCases: len(all[armBaseline]) + len(all[armEvolution]) + len(all[armOptimized]),
			Families:      append([]string(nil), protocol.ExpectedFamilies...),
			Scales:        append([]string(nil), protocol.ExpectedScales...),
		},
		Arms:     make(map[string]Metrics, 3),
		Families: make(map[string]map[string]Metrics, len(protocol.ExpectedFamilies)),
	}
	for arm, cases := range all {
		evidence.Arms[arm] = summarize(cases)
	}
	for _, family := range protocol.ExpectedFamilies {
		evidence.Families[family] = make(map[string]Metrics, 3)
		for arm, cases := range all {
			evidence.Families[family][arm] = summarize(filterFamily(cases, family))
		}
	}
	evidence.EvolutionVsBaseline = compare(
		armBaseline, armEvolution, all[armBaseline], all[armEvolution],
	)
	evidence.OptimizedEvolutionVsEvolution = compare(
		armEvolution, armOptimized, all[armEvolution], all[armOptimized],
	)
	evidence.Gates = promotionGates(evidence)
	evidence.PromotionEligible = true
	for _, gate := range evidence.Gates {
		if !gate.Passed {
			evidence.PromotionEligible = false
			break
		}
	}
	if evidence.PromotionEligible {
		evidence.PromotionReason = "all preregistered coverage, non-regression, and meaningful-benefit gates passed"
	} else {
		evidence.PromotionReason = "one or more preregistered gates failed; inspect gates for details"
	}
	return evidence, nil
}

func summarizeRun(
	path string,
	input inputResult,
	cases map[string][]collectedCase,
) RunSummary {
	arms := make(map[string]Metrics, len(cases))
	for arm, items := range cases {
		arms[arm] = summarize(items)
	}
	return RunSummary{
		Name:     filepath.Base(filepath.Dir(path)),
		RootSeed: *input.EvaluationSeed,
		RunOrder: append([]string(nil), input.RunOrder...),
		Arms:     arms,
		EvolutionVsBaseline: compare(
			armBaseline, armEvolution, cases[armBaseline], cases[armEvolution],
		),
		OptimizedEvolutionVsEvolution: compare(
			armEvolution, armOptimized, cases[armEvolution], cases[armOptimized],
		),
	}
}

func validateProtocol(p Protocol) error {
	if strings.TrimSpace(p.Name) == "" || len(p.ExpectedFamilies) == 0 ||
		len(p.ExpectedScales) == 0 || p.MinimumRuns <= 0 {
		return errors.New("experiment: incomplete protocol")
	}
	if p.OverallQualityTolerancePP < 0 || p.FamilyQualityTolerancePP < 0 ||
		p.MeaningfulQualityBenefitPP < 0 || p.MeaningfulTokenReduction < 0 ||
		p.MeaningfulTokenReduction >= 1 {
		return errors.New("experiment: invalid gate threshold")
	}
	return nil
}

func loadInput(path string) (inputResult, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return inputResult{}, fmt.Errorf("experiment: read %s: %w", path, err)
	}
	var input inputResult
	if err := json.Unmarshal(raw, &input); err != nil {
		return inputResult{}, fmt.Errorf("experiment: decode %s: %w", path, err)
	}
	return input, nil
}

func inputArms(input inputResult) map[string][]inputCase {
	return map[string][]inputCase{
		armBaseline:  input.Baseline.Cases,
		armEvolution: input.Evolution.Cases,
		armOptimized: input.OptimizedEvolution.Cases,
	}
}

func validateInput(input inputResult, p Protocol) error {
	if input.Baseline == nil || input.Evolution == nil || input.OptimizedEvolution == nil {
		return errors.New("requires baseline, evolution, and optimizedEvolution arms")
	}
	expected := make(map[string]struct{}, len(p.ExpectedFamilies)*len(p.ExpectedScales))
	for _, family := range p.ExpectedFamilies {
		for _, scale := range p.ExpectedScales {
			expected[family+"/"+scale] = struct{}{}
		}
	}
	seedByTask := make(map[string]int64, len(expected))
	for arm, cases := range inputArms(input) {
		if len(cases) != len(expected) {
			return fmt.Errorf("%s has %d cases; expected %d", arm, len(cases), len(expected))
		}
		seen := make(map[string]struct{}, len(cases))
		for _, item := range cases {
			if item.Evaluation == nil {
				return fmt.Errorf("%s/%s has no official evaluation", arm, item.TaskID)
			}
			if item.EvaluationSeed == nil {
				return fmt.Errorf("%s/%s has no paired evaluation seed", arm, item.TaskID)
			}
			key := item.BaseTask + "/" + item.Scale
			if _, ok := expected[key]; !ok || item.TaskID != key {
				return fmt.Errorf("%s contains unexpected task %q", arm, item.TaskID)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%s contains duplicate task %q", arm, key)
			}
			seen[key] = struct{}{}
			if seed, ok := seedByTask[key]; ok && seed != *item.EvaluationSeed {
				return fmt.Errorf("task %s is not seed-paired across arms", key)
			}
			seedByTask[key] = *item.EvaluationSeed
		}
	}
	return nil
}

func summarize(cases []collectedCase) Metrics {
	metrics := Metrics{Tasks: len(cases)}
	if len(cases) == 0 {
		return metrics
	}
	var quality, agent, reviewer, endToEnd, tools, duration float64
	for _, item := range cases {
		if item.data.Evaluation.Passed {
			metrics.PassedTasks++
		}
		quality += item.data.Evaluation.Score.Percent
		agent += float64(item.data.TotalTokens)
		reviewer += float64(item.data.ReviewerTotalTokens)
		endToEnd += float64(item.data.EndToEndTotalTokens)
		tools += float64(len(item.data.ToolCalls))
		duration += item.data.DurationSeconds
	}
	count := float64(len(cases))
	metrics.PassRate = round(float64(metrics.PassedTasks) / count * 100)
	metrics.AverageQuality = round(quality / count)
	metrics.AverageAgentTokens = round(agent / count)
	metrics.AverageReviewerTokens = round(reviewer / count)
	metrics.AverageEndToEndTokens = round(endToEnd / count)
	metrics.AverageToolCalls = round(tools / count)
	metrics.AverageDurationSec = round(duration / count)
	return metrics
}

func filterFamily(cases []collectedCase, family string) []collectedCase {
	out := make([]collectedCase, 0, len(cases))
	for _, item := range cases {
		if item.data.BaseTask == family {
			out = append(out, item)
		}
	}
	return out
}

func compare(controlName, candidateName string, control, candidate []collectedCase) Comparison {
	controlMetrics := summarize(control)
	candidateMetrics := summarize(candidate)
	delta := Delta{
		PassRatePP:     round(candidateMetrics.PassRate - controlMetrics.PassRate),
		QualityPP:      round(candidateMetrics.AverageQuality - controlMetrics.AverageQuality),
		AgentTokens:    round(candidateMetrics.AverageAgentTokens - controlMetrics.AverageAgentTokens),
		ReviewerTokens: round(candidateMetrics.AverageReviewerTokens - controlMetrics.AverageReviewerTokens),
		EndToEndTokens: round(candidateMetrics.AverageEndToEndTokens - controlMetrics.AverageEndToEndTokens),
		ToolCalls:      round(candidateMetrics.AverageToolCalls - controlMetrics.AverageToolCalls),
		DurationSec:    round(candidateMetrics.AverageDurationSec - controlMetrics.AverageDurationSec),
	}
	if controlMetrics.AverageEndToEndTokens != 0 {
		delta.EndToEndTokensPC = round(delta.EndToEndTokens / controlMetrics.AverageEndToEndTokens * 100)
	}
	return Comparison{
		Control:   controlName,
		Candidate: candidateName,
		Delta:     delta,
		Pairs:     comparePairs(control, candidate),
	}
}

func comparePairs(control, candidate []collectedCase) PairSummary {
	controlByKey := make(map[string]inputCase, len(control))
	for _, item := range control {
		controlByKey[fmt.Sprintf("%d/%s", item.run, item.data.TaskID)] = item.data
	}
	var result PairSummary
	for _, item := range candidate {
		controlCase, ok := controlByKey[fmt.Sprintf("%d/%s", item.run, item.data.TaskID)]
		if !ok {
			continue
		}
		result.Pairs++
		switch {
		case item.data.Evaluation.Passed && !controlCase.Evaluation.Passed:
			result.PassWins++
		case !item.data.Evaluation.Passed && controlCase.Evaluation.Passed:
			result.PassLosses++
		default:
			result.PassTies++
		}
		delta := item.data.Evaluation.Score.Percent - controlCase.Evaluation.Score.Percent
		switch {
		case delta > 1e-9:
			result.QualityWins++
		case delta < -1e-9:
			result.QualityLosses++
		default:
			result.QualityTies++
		}
	}
	return result
}

func promotionGates(e *Evidence) []GateCheck {
	p := e.Protocol
	control := e.Arms[armEvolution]
	candidate := e.Arms[armOptimized]
	gates := []GateCheck{
		{
			Name:   "coverage",
			Passed: e.Coverage.Runs >= p.MinimumRuns,
			Details: fmt.Sprintf("%d run(s), minimum %d; each arm has %d expected tasks per run",
				e.Coverage.Runs, p.MinimumRuns, e.Coverage.TasksPerArm),
		},
		{
			Name:    "overall-pass-non-regression",
			Passed:  candidate.PassRate >= control.PassRate,
			Details: fmt.Sprintf("optimized %.2f%% vs evolution %.2f%%", candidate.PassRate, control.PassRate),
		},
		{
			Name:   "overall-quality-non-regression",
			Passed: candidate.AverageQuality+p.OverallQualityTolerancePP >= control.AverageQuality,
			Details: fmt.Sprintf("optimized %.2f vs evolution %.2f; tolerance %.2fpp",
				candidate.AverageQuality, control.AverageQuality, p.OverallQualityTolerancePP),
		},
	}
	familyPass := true
	familyQuality := true
	var passFailures, qualityFailures []string
	for _, family := range p.ExpectedFamilies {
		familyControl := e.Families[family][armEvolution]
		familyCandidate := e.Families[family][armOptimized]
		if familyCandidate.PassRate < familyControl.PassRate {
			familyPass = false
			passFailures = append(passFailures, fmt.Sprintf("%s %.2f<%.2f", family, familyCandidate.PassRate, familyControl.PassRate))
		}
		if familyCandidate.AverageQuality+p.FamilyQualityTolerancePP < familyControl.AverageQuality {
			familyQuality = false
			qualityFailures = append(qualityFailures, fmt.Sprintf("%s %.2f<%.2f", family, familyCandidate.AverageQuality, familyControl.AverageQuality))
		}
	}
	gates = append(gates,
		GateCheck{
			Name:    "family-pass-non-regression",
			Passed:  familyPass,
			Details: gateDetails(passFailures),
		},
		GateCheck{
			Name:    "family-quality-non-regression",
			Passed:  familyQuality,
			Details: gateDetails(qualityFailures),
		},
	)
	qualityBenefit := candidate.AverageQuality-control.AverageQuality >= p.MeaningfulQualityBenefitPP
	tokenBenefit := control.AverageEndToEndTokens > 0 &&
		candidate.AverageEndToEndTokens <= control.AverageEndToEndTokens*(1-p.MeaningfulTokenReduction)
	gates = append(gates, GateCheck{
		Name:   "meaningful-benefit",
		Passed: qualityBenefit || tokenBenefit,
		Details: fmt.Sprintf("quality delta %.2fpp (need +%.2fpp) or end-to-end token delta %.2f%% (need -%.2f%%)",
			candidate.AverageQuality-control.AverageQuality,
			p.MeaningfulQualityBenefitPP,
			e.OptimizedEvolutionVsEvolution.Delta.EndToEndTokensPC,
			p.MeaningfulTokenReduction*100),
	})
	return gates
}

func gateDetails(failures []string) string {
	if len(failures) == 0 {
		return "all expected families passed"
	}
	return "failed: " + strings.Join(failures, "; ")
}

func round(value float64) float64 {
	return math.Round(value*100) / 100
}
