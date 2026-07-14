//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func appendOptimizationSection(b *strings.Builder, run *optimizationBenchmarkResult) {
	if run == nil || run.Result == nil {
		return
	}
	result := run.Result
	b.WriteString("\n## Reflective Optimization\n\n")
	b.WriteString("### Decision\n\n")
	fmt.Fprintf(b, "%s\n\n", optimizationConclusion(run))
	fmt.Fprintf(b, "- Promotion eligible: `%t`\n", result.PromotionEligible)
	fmt.Fprintf(b, "- Promotion reason: `%s`\n", result.PromotionReason)
	fmt.Fprintf(b, "- Selected skill differs from seed: `%t`\n", run.SelectedChanged)

	b.WriteString("\n### Experiment\n\n")
	fmt.Fprintf(b, "- Started: `%s`\n", run.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(b, "- Finished: `%s`\n", run.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(b, "- Seed spec: `%s`\n", run.SeedSpecPath)
	fmt.Fprintf(b, "- Feedback scales: `%s`\n", strings.Join(run.FeedbackScales, ","))
	fmt.Fprintf(b, "- Validation scales: `%s`\n", strings.Join(run.ValidationScales, ","))
	fmt.Fprintf(b, "- Holdout scales: `%s`\n", strings.Join(run.HoldoutScales, ","))
	fmt.Fprintf(b, "- Repeats per task: `%d`\n", run.Repeats)
	fmt.Fprintf(b, "- Accepted candidates including seed: `%d`\n", result.CandidateCount)
	fmt.Fprintf(b, "- Evaluated cases: `%d`\n", result.MetricCalls)
	fmt.Fprintf(b, "- Agent tokens: `%d`\n", run.AgentTokens)
	fmt.Fprintf(b, "- Reflection tokens: `%d`\n", run.ReflectionUsage.TotalTokens)
	fmt.Fprintf(b, "- Stop reason: `%s`\n", result.StopReason)

	b.WriteString("\n### Paired Scores\n\n")
	b.WriteString("| Split | Seed | Selected | Delta |\n")
	b.WriteString("|---|---:|---:|---:|\n")
	appendOptimizationScoreRow(b, "Validation", result.BaselineValidation.Score, result.CandidateValidation.Score)
	appendOptimizationScoreRow(b, "Holdout", result.BaselineHoldout.Score, result.CandidateHoldout.Score)

	b.WriteString("\n### Holdout Objectives\n\n")
	b.WriteString("| Objective | Preferred | Seed | Selected | Delta |\n")
	b.WriteString("|---|---|---:|---:|---:|\n")
	keys := make([]string, 0, len(result.BaselineHoldout.Objectives))
	for key := range result.BaselineHoldout.Objectives {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		baseline := result.BaselineHoldout.Objectives[key]
		candidate := result.CandidateHoldout.Objectives[key]
		fmt.Fprintf(
			b,
			"| `%s` | %s | %.4f | %.4f | %+.4f |\n",
			key,
			objectivePreference(key),
			baseline,
			candidate,
			candidate-baseline,
		)
	}
	b.WriteString("\nThe validation-selected skill is under `selected_skill/`; its presence does not imply promotion eligibility. Candidate lineage, paired seeds, evaluator feedback, and traces are under `optimization_experiments/`.\n")
}

func optimizationConclusion(run *optimizationBenchmarkResult) string {
	result := run.Result
	if result.PromotionEligible {
		return "The selected candidate passed the configured holdout gate and is eligible for promotion."
	}
	reason := strings.TrimSpace(result.PromotionReason)
	if reason == "" {
		reason = "the configured holdout gate did not approve it"
	}
	if !run.SelectedChanged {
		return fmt.Sprintf("The search retained the seed skill; no candidate was promoted because %s.", reason)
	}
	return fmt.Sprintf("The validation-selected candidate is not eligible for promotion because %s.", reason)
}

func appendOptimizationScoreRow(b *strings.Builder, name string, baseline, candidate float64) {
	fmt.Fprintf(
		b,
		"| %s | %.4f | %.4f | %+.4f |\n",
		name,
		baseline,
		candidate,
		candidate-baseline,
	)
}

func objectivePreference(name string) string {
	switch name {
	case objectiveAgentTokens, objectiveToolCalls, objectiveDuration:
		return "lower"
	default:
		return "higher"
	}
}
