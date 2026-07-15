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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type evaluatorFunc func(
	context.Context,
	*evolution.SkillSpec,
	[]framework.Case,
	int64,
) ([]framework.Evaluation, error)

func (f evaluatorFunc) Evaluate(
	ctx context.Context,
	spec *evolution.SkillSpec,
	cases []framework.Case,
	seed int64,
) ([]framework.Evaluation, error) {
	return f(ctx, spec, cases, seed)
}

func TestRunFixedComparison(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	type call struct {
		caseID    string
		seed      int64
		candidate bool
	}
	var calls []call
	evaluator := evaluatorFunc(func(
		_ context.Context,
		spec *evolution.SkillSpec,
		cases []framework.Case,
		pairedSeed int64,
	) ([]framework.Evaluation, error) {
		require.Len(t, cases, 1)
		calls = append(calls, call{
			caseID: cases[0].ID, seed: pairedSeed,
			candidate: spec.Description == "candidate",
		})
		score, tokens := 0.8, 100.0
		if spec.Description == "candidate" {
			score, tokens = 0.9, 80
		}
		return []framework.Evaluation{{
			CaseID: cases[0].ID,
			Score:  score,
			Objectives: map[string]float64{
				objectiveQuality:     score,
				objectivePassed:      1,
				objectiveAgentTokens: tokens,
			},
		}}, nil
	})
	dataset := framework.Dataset{
		Validation: []framework.Case{{ID: "validation-a"}, {ID: "validation-b"}},
		Holdout:    []framework.Case{{ID: "holdout", Critical: true}},
	}
	outcome, err := run(context.Background(), Request{
		Evaluator: evaluator,
		Config:    Config{RandomSeed: 17},
		Seed:      seed,
		Candidate: candidate,
		Dataset:   dataset,
	}, func(model.Model, framework.Evaluator, ...framework.Option) (runner, error) {
		t.Fatal("reflective optimizer must not be constructed for fixed comparison")
		return nil, nil
	})
	require.NoError(t, err)
	require.True(t, outcome.Search.PromotionEligible)
	require.Equal(t, "fixed_candidate", outcome.Search.StopReason)
	require.Equal(t, 6, outcome.Search.MetricCalls)
	require.Len(t, outcome.Comparison.Validation, 2)
	require.Len(t, outcome.Comparison.Holdout, 1)
	require.Len(t, calls, 6)
	require.Equal(t, calls[0].caseID, calls[1].caseID)
	require.Equal(t, calls[0].seed, calls[1].seed)
	require.Equal(t, calls[2].caseID, calls[3].caseID)
	require.Equal(t, calls[2].seed, calls[3].seed)
	require.NotEqual(t, calls[0].candidate, calls[2].candidate)
	require.Equal(t, calls[4].caseID, calls[5].caseID)
	require.Equal(t, calls[4].seed, calls[5].seed)
}

func TestFixedComparisonRejectsRegression(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	evaluator := evaluatorFunc(func(
		_ context.Context,
		spec *evolution.SkillSpec,
		cases []framework.Case,
		_ int64,
	) ([]framework.Evaluation, error) {
		score := 0.8
		if spec.Description == "candidate" && cases[0].ID == "holdout" {
			score = 0.7
		}
		return []framework.Evaluation{{CaseID: cases[0].ID, Score: score}}, nil
	})
	outcome, err := Run(context.Background(), Request{
		Evaluator: evaluator,
		Seed:      seed,
		Candidate: candidate,
		Dataset: framework.Dataset{
			Validation: []framework.Case{{ID: "validation"}},
			Holdout:    []framework.Case{{ID: "holdout", Critical: true}},
		},
	})
	require.NoError(t, err)
	require.False(t, outcome.Search.PromotionEligible)
	require.Contains(t, outcome.Search.PromotionReason, "holdout")
}

func TestFixedComparisonKeepsHoldoutEvidenceAfterValidationRegression(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	var calls int
	evaluator := evaluatorFunc(func(
		_ context.Context,
		spec *evolution.SkillSpec,
		cases []framework.Case,
		_ int64,
	) ([]framework.Evaluation, error) {
		calls++
		score := 0.8
		if spec.Description == "candidate" && cases[0].ID == "validation" {
			score = 0.7
		}
		return []framework.Evaluation{{CaseID: cases[0].ID, Score: score}}, nil
	})
	outcome, err := Run(context.Background(), Request{
		Evaluator: evaluator,
		Seed:      seed,
		Candidate: candidate,
		Dataset: framework.Dataset{
			Validation: []framework.Case{{ID: "validation"}},
			Holdout:    []framework.Case{{ID: "holdout", Critical: true}},
		},
	})
	require.NoError(t, err)
	require.False(t, outcome.Search.PromotionEligible)
	require.Contains(t, outcome.Search.PromotionReason, "validation")
	require.Equal(t, 4, outcome.Search.MetricCalls)
	require.Equal(t, 4, calls)
	require.Len(t, outcome.Comparison.Holdout, 1)
	require.Equal(t, 1, outcome.Search.BaselineHoldout.Cases)
}

func TestFixedComparisonRejectsQualityRegressionDespiteHigherScore(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	var calls int
	evaluator := evaluatorFunc(func(
		_ context.Context,
		spec *evolution.SkillSpec,
		cases []framework.Case,
		_ int64,
	) ([]framework.Evaluation, error) {
		calls++
		score, quality := 0.8, 0.9
		if spec.Description == "candidate" {
			score, quality = 0.9, 0.8
		}
		return []framework.Evaluation{{
			CaseID: cases[0].ID,
			Score:  score,
			Objectives: map[string]float64{
				objectivePassed:  1,
				objectiveQuality: quality,
			},
		}}, nil
	})
	outcome, err := Run(context.Background(), Request{
		Evaluator: evaluator,
		Seed:      seed,
		Candidate: candidate,
		Dataset: framework.Dataset{
			Validation: []framework.Case{{ID: "validation"}},
			Holdout:    []framework.Case{{ID: "holdout", Critical: true}},
		},
	})
	require.NoError(t, err)
	require.False(t, outcome.Search.PromotionEligible)
	require.Equal(
		t,
		"frozen candidate regressed on validation official_quality",
		outcome.Search.PromotionReason,
	)
	require.Equal(t, 4, calls)
	require.Len(t, outcome.Comparison.Holdout, 1)
}

func TestFixedComparisonValidation(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	validDataset := framework.Dataset{
		Validation: []framework.Case{{ID: "validation"}},
		Holdout:    []framework.Case{{ID: "holdout"}},
	}

	_, err := Run(context.Background(), Request{
		Seed: seed, Candidate: candidate, Dataset: validDataset,
	})
	require.ErrorContains(t, err, "requires an evaluator")
	_, err = Run(context.Background(), Request{
		Evaluator: evaluatorFunc(nil), Seed: seed, Candidate: seed, Dataset: validDataset,
	})
	require.ErrorContains(t, err, "identical")

	badScore := evaluatorFunc(func(
		_ context.Context,
		_ *evolution.SkillSpec,
		cases []framework.Case,
		_ int64,
	) ([]framework.Evaluation, error) {
		return []framework.Evaluation{{CaseID: cases[0].ID, Score: math.NaN()}}, nil
	})
	_, err = Run(context.Background(), Request{
		Evaluator: badScore, Seed: seed, Candidate: candidate, Dataset: validDataset,
	})
	require.ErrorContains(t, err, "finite")

	_, err = Run(context.Background(), Request{
		Evaluator: evaluatorFunc(nil),
		Config:    Config{MaxMetricCalls: 3},
		Seed:      seed,
		Candidate: candidate,
		Dataset:   validDataset,
	})
	require.ErrorContains(t, err, "requires 4 metric calls")
}

func TestFixedComparisonHonorsTimeLimit(t *testing.T) {
	seed := testSpec()
	candidate := testSpec()
	candidate.Description = "candidate"
	evaluator := evaluatorFunc(func(
		ctx context.Context,
		_ *evolution.SkillSpec,
		_ []framework.Case,
		_ int64,
	) ([]framework.Evaluation, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_, err := Run(context.Background(), Request{
		Evaluator: evaluator,
		Config:    Config{TimeLimit: time.Millisecond},
		Seed:      seed,
		Candidate: candidate,
		Dataset: framework.Dataset{
			Validation: []framework.Case{{ID: "validation"}},
			Holdout:    []framework.Case{{ID: "holdout"}},
		},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
