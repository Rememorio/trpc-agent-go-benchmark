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
		Repeats:          1,
		SelectedChanged:  true,
		Search: &framework.Result{
			CandidateCount:      2,
			MetricCalls:         8,
			StopReason:          "max_iterations",
			PromotionEligible:   true,
			PromotionReason:     "holdout requirements satisfied",
			BaselineValidation:  framework.Summary{Score: 0.8},
			CandidateValidation: framework.Summary{Score: 0.9},
			BaselineHoldout: framework.Summary{
				Score:      0.7,
				Objectives: map[string]float64{objectiveQuality: 0.8},
			},
			CandidateHoldout: framework.Summary{
				Score:      0.9,
				Objectives: map[string]float64{objectiveQuality: 1},
			},
		},
	}
	var report strings.Builder
	AppendReport(&report, run)
	require.Contains(t, report.String(), "eligible for promotion")
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
