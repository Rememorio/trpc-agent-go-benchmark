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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
)

func TestAppendReport(t *testing.T) {
	run := &Result{
		SeedSpec:         "seed.json",
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		CriticalScales:   []string{"e3"},
		Repeats:          1,
		RandomSeed:       17,
		SelectedChanged:  true,
		Search: &framework.Result{
			Algorithm:           "gepa",
			CandidateCount:      2,
			MetricCalls:         8,
			StopReason:          "max_iterations",
			PromotionEligible:   true,
			PromotionReason:     "holdout requirements satisfied",
			BaselineValidation:  framework.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: framework.Summary{Score: 0.9, Cases: 1},
			BaselineHoldout: framework.Summary{
				Score:      0.7,
				Cases:      1,
				Objectives: map[string]float64{objectiveQuality: 0.8},
			},
			CandidateHoldout: framework.Summary{
				Score:      0.9,
				Cases:      1,
				Objectives: map[string]float64{objectiveQuality: 1},
			},
		},
	}
	var report strings.Builder
	AppendReport(&report, run)
	require.Contains(t, report.String(), "eligible for promotion")
	require.Contains(t, report.String(), "- Optimizer: `gepa`")
	require.Contains(t, report.String(), "- Random seed: `17`")
	require.Contains(t, report.String(), "- Critical holdout scales: `e3`")
	require.Contains(t, report.String(), "- Evaluation temperature: `0.00`")
	require.Contains(t, report.String(), "| Holdout | 0.7000 | 0.9000 | +0.2000 |")
	require.Contains(t, report.String(), "| `official_quality` | higher | 0.8000 | 1.0000 | +0.2000 |")

	run.Search.PromotionEligible = false
	run.Search.PromotionReason = "holdout score regressed"
	require.Contains(t, conclusion(run), "not eligible for promotion")
	run.SelectedChanged = false
	run.Search.PromotionReason = ""
	require.Contains(t, conclusion(run), "retained the seed skill")
	require.Contains(t, conclusion(run), "holdout gate did not approve")

	var empty strings.Builder
	AppendReport(&empty, nil)
	require.Empty(t, empty.String())
	require.Equal(t, "lower", objectivePreference(objectiveAgentTokens))
	require.Equal(t, "higher", objectivePreference(objectivePassed))
}

func TestAppendReportFrozenComparison(t *testing.T) {
	run := &Result{
		SeedSpec:         "seed.json",
		CandidateSpec:    "candidate.json",
		FeedbackScales:   []string{"e1"},
		ValidationScales: []string{"e2"},
		HoldoutScales:    []string{"e3"},
		Repeats:          2,
		RandomSeed:       23,
		SelectedChanged:  true,
		Search: &framework.Result{
			CandidateCount:      2,
			MetricCalls:         4,
			StopReason:          "fixed_candidate",
			PromotionEligible:   true,
			PromotionReason:     "frozen candidate passed validation and holdout",
			BaselineValidation:  framework.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: framework.Summary{Score: 0.9, Cases: 1},
			BaselineHoldout: framework.Summary{
				Score: 0.7,
				Cases: 1,
				Objectives: map[string]float64{
					objectiveQuality:     0.8,
					objectivePassed:      1,
					objectiveAgentTokens: 100,
				},
			},
			CandidateHoldout: framework.Summary{
				Score: 0.9,
				Cases: 1,
				Objectives: map[string]float64{
					objectiveQuality:     0.9,
					objectivePassed:      1,
					objectiveAgentTokens: 80,
				},
			},
		},
		Comparison: &Comparison{
			Holdout: []Pair{{
				CaseID: "holdout/task/r1",
				Baseline: framework.Evaluation{
					Score: 0.7,
					Objectives: map[string]float64{
						objectiveQuality:     0.8,
						objectivePassed:      1,
						objectiveAgentTokens: 100,
					},
				},
				Candidate: framework.Evaluation{
					Score: 0.9,
					Objectives: map[string]float64{
						objectiveQuality:     0.9,
						objectivePassed:      1,
						objectiveAgentTokens: 80,
					},
				},
			}},
		},
	}

	var report strings.Builder
	AppendReport(&report, run)
	text := report.String()
	require.Contains(t, text, "frozen candidate passed")
	require.Contains(t, text, "- Frozen candidate spec: `candidate.json`")
	require.Contains(t, text, "- Compared specs: `2`")
	require.Contains(t, text, "| Holdout | `holdout/task/r1` | 0.7000 | 0.9000 | +0.1000 | +0 | -20 |")
	require.NotContains(t, text, "Accepted candidates including seed")

	run.Search.PromotionEligible = false
	run.Search.PromotionReason = "holdout regressed"
	require.Contains(t, conclusion(run), "not eligible for promotion")
}

func TestAppendReportFrozenValidationFailure(t *testing.T) {
	run := &Result{
		Search: &framework.Result{
			BaselineValidation:  framework.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: framework.Summary{Score: 0.7, Cases: 1},
			PromotionReason:     "frozen candidate regressed on validation",
		},
		Comparison: &Comparison{},
	}
	var report strings.Builder
	AppendReport(&report, run)
	require.Contains(t, report.String(), "Holdout was not evaluated")
	require.NotContains(t, report.String(), "### Holdout Objectives")
}

func TestAppendReportSearchWithoutHoldout(t *testing.T) {
	run := &Result{
		Search: &framework.Result{
			BaselineValidation:  framework.Summary{Score: 0.8, Cases: 1},
			CandidateValidation: framework.Summary{Score: 0.9, Cases: 1},
			PromotionReason:     "no holdout split configured",
		},
	}
	var report strings.Builder
	AppendReport(&report, run)
	require.Contains(t, report.String(), "No holdout split was configured for this search")
	require.NotContains(t, report.String(), "failed validation")
}
