//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

// Pair records one frozen seed/candidate evaluation with a shared evaluator
// seed. It is emitted only for fixed comparisons, not reflective search.
type Pair struct {
	CaseID    string               `json:"caseId"`
	Seed      int64                `json:"seed"`
	Baseline  framework.Evaluation `json:"baseline"`
	Candidate framework.Evaluation `json:"candidate"`
}

// Comparison contains the per-case evidence for a frozen candidate A/B.
type Comparison struct {
	Validation []Pair `json:"validation"`
	Holdout    []Pair `json:"holdout"`
}

func runComparison(ctx context.Context, request Request) (*Outcome, error) {
	if request.Evaluator == nil {
		return nil, errors.New("fixed comparison requires an evaluator")
	}
	if request.Seed == nil || request.Candidate == nil {
		return nil, errors.New("fixed comparison requires seed and candidate specs")
	}
	if reflect.DeepEqual(request.Seed, request.Candidate) {
		return nil, errors.New("fixed comparison candidate is identical to seed")
	}
	if len(request.Dataset.Validation) == 0 || len(request.Dataset.Holdout) == 0 {
		return nil, errors.New("fixed comparison requires validation and holdout cases")
	}
	requiredCalls := 2 * (len(request.Dataset.Validation) + len(request.Dataset.Holdout))
	if request.Config.MaxMetricCalls > 0 && requiredCalls > request.Config.MaxMetricCalls {
		return nil, fmt.Errorf(
			"fixed comparison requires %d metric calls, budget allows %d",
			requiredCalls,
			request.Config.MaxMetricCalls,
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.Config.TimeLimit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Config.TimeLimit)
		defer cancel()
	}
	// #nosec G404 -- deterministic experiment pairing is not security-sensitive.
	rng := rand.New(rand.NewSource(request.Config.RandomSeed))
	validation, baselineValidation, candidateValidation, err := compareCases(
		ctx, request.Evaluator, request.Seed, request.Candidate,
		request.Dataset.Validation, rng,
	)
	if err != nil {
		return nil, fmt.Errorf("compare validation: %w", err)
	}
	result := &framework.Result{
		Spec:                cloneSpec(request.Candidate),
		BaselineValidation:  baselineValidation,
		CandidateValidation: candidateValidation,
		CandidateCount:      2,
		MetricCalls:         2 * len(validation),
		StopReason:          "fixed_candidate",
	}
	comparison := &Comparison{Validation: validation}
	validationReason := summaryRegression(
		"validation", baselineValidation, candidateValidation,
	)
	holdout, baselineHoldout, candidateHoldout, err := compareCases(
		ctx, request.Evaluator, request.Seed, request.Candidate,
		request.Dataset.Holdout, rng,
	)
	if err != nil {
		return nil, fmt.Errorf("compare holdout: %w", err)
	}
	result.BaselineHoldout = baselineHoldout
	result.CandidateHoldout = candidateHoldout
	result.MetricCalls += 2 * len(holdout)
	comparison.Holdout = holdout
	if validationReason != "" {
		result.PromotionReason = validationReason
	} else {
		assessComparison(request.Dataset, holdout, result)
	}
	return &Outcome{Search: result, Comparison: comparison}, nil
}

func compareCases(
	ctx context.Context,
	evaluator framework.Evaluator,
	baseline *evolution.SkillSpec,
	candidate *evolution.SkillSpec,
	cases []framework.Case,
	rng *rand.Rand,
) ([]Pair, framework.Summary, framework.Summary, error) {
	pairs := make([]Pair, 0, len(cases))
	candidateStarts := rng.Intn(2) == 0
	for i, item := range cases {
		if err := ctx.Err(); err != nil {
			return nil, framework.Summary{}, framework.Summary{}, err
		}
		seed := rng.Int63()
		var baselineResult, candidateResult framework.Evaluation
		var err error
		if candidateStarts == (i%2 == 0) {
			candidateResult, err = evaluateOne(ctx, evaluator, candidate, item, seed)
			if err == nil {
				baselineResult, err = evaluateOne(ctx, evaluator, baseline, item, seed)
			}
		} else {
			baselineResult, err = evaluateOne(ctx, evaluator, baseline, item, seed)
			if err == nil {
				candidateResult, err = evaluateOne(ctx, evaluator, candidate, item, seed)
			}
		}
		if err != nil {
			return nil, framework.Summary{}, framework.Summary{}, err
		}
		pairs = append(pairs, Pair{
			CaseID: item.ID, Seed: seed,
			Baseline: baselineResult, Candidate: candidateResult,
		})
	}
	return pairs, summarizePairs(pairs, false), summarizePairs(pairs, true), nil
}

func evaluateOne(
	ctx context.Context,
	evaluator framework.Evaluator,
	spec *evolution.SkillSpec,
	item framework.Case,
	seed int64,
) (framework.Evaluation, error) {
	results, err := evaluator.Evaluate(
		ctx, cloneSpec(spec), []framework.Case{cloneCase(item)}, seed,
	)
	if err != nil {
		return framework.Evaluation{}, err
	}
	if len(results) != 1 {
		return framework.Evaluation{}, fmt.Errorf(
			"case %q returned %d evaluations", item.ID, len(results),
		)
	}
	result := results[0]
	if result.CaseID != item.ID {
		return framework.Evaluation{}, fmt.Errorf(
			"case %q returned evaluation for %q", item.ID, result.CaseID,
		)
	}
	if math.IsNaN(result.Score) || math.IsInf(result.Score, 0) ||
		result.Score < 0 || result.Score > 1 {
		return framework.Evaluation{}, fmt.Errorf(
			"case %q score must be finite and within [0, 1]", item.ID,
		)
	}
	for name, value := range result.Objectives {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return framework.Evaluation{}, fmt.Errorf(
				"case %q objective %q must be finite", item.ID, name,
			)
		}
	}
	result.Objectives = cloneObjectives(result.Objectives)
	return result, nil
}

func summarizePairs(pairs []Pair, candidate bool) framework.Summary {
	summary := framework.Summary{Cases: len(pairs)}
	if len(pairs) == 0 {
		return summary
	}
	totals := make(map[string]float64)
	counts := make(map[string]int)
	for _, pair := range pairs {
		result := pair.Baseline
		if candidate {
			result = pair.Candidate
		}
		summary.Score += result.Score
		for name, value := range result.Objectives {
			totals[name] += value
			counts[name]++
		}
	}
	summary.Score /= float64(len(pairs))
	if len(totals) > 0 {
		summary.Objectives = make(map[string]float64, len(totals))
		for name, total := range totals {
			summary.Objectives[name] = total / float64(counts[name])
		}
	}
	return summary
}

func assessComparison(
	dataset framework.Dataset,
	holdout []Pair,
	result *framework.Result,
) {
	if reason := summaryRegression(
		"holdout", result.BaselineHoldout, result.CandidateHoldout,
	); reason != "" {
		result.PromotionReason = reason
		return
	}
	critical := make(map[string]bool)
	for _, item := range dataset.Holdout {
		critical[item.ID] = item.Critical
	}
	for _, pair := range holdout {
		if !critical[pair.CaseID] {
			continue
		}
		if reason := evaluationRegression(pair.Baseline, pair.Candidate); reason != "" {
			result.PromotionReason = fmt.Sprintf(
				"critical holdout case %s regressed on %s", pair.CaseID, reason,
			)
			return
		}
	}
	result.PromotionEligible = true
	result.PromotionReason = "frozen candidate passed validation and holdout"
}

func summaryRegression(
	split string,
	baseline framework.Summary,
	candidate framework.Summary,
) string {
	for _, objective := range []string{objectivePassed, objectiveQuality} {
		if candidate.Objectives[objective] < baseline.Objectives[objective] {
			return fmt.Sprintf("frozen candidate regressed on %s %s", split, objective)
		}
	}
	if candidate.Score < baseline.Score {
		return "frozen candidate regressed on " + split + " score"
	}
	return ""
}

func evaluationRegression(
	baseline framework.Evaluation,
	candidate framework.Evaluation,
) string {
	for _, objective := range []string{objectivePassed, objectiveQuality} {
		if candidate.Objectives[objective] < baseline.Objectives[objective] {
			return objective
		}
	}
	if candidate.Score < baseline.Score {
		return "score"
	}
	return ""
}

func cloneSpec(spec *evolution.SkillSpec) *evolution.SkillSpec {
	if spec == nil {
		return nil
	}
	result := *spec
	result.Steps = append([]string(nil), spec.Steps...)
	result.Pitfalls = append([]string(nil), spec.Pitfalls...)
	return &result
}

func cloneCase(item framework.Case) framework.Case {
	metadata := item.Metadata
	item.Metadata = make(map[string]string, len(metadata))
	for name, value := range metadata {
		item.Metadata[name] = value
	}
	return item
}

func cloneObjectives(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	result := make(map[string]float64, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
