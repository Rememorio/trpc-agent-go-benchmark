//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestCompareLongMemEvalReplicates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := writeLongMemEvalReplicateFixture(t, dir)
	outputDir := filepath.Join(dir, "output")
	if err := compareLongMemEvalReplicates(manifestPath, outputDir); err != nil {
		t.Fatalf("compare replicates: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "replicate_comparison.json"))
	if err != nil {
		t.Fatalf("read replicate comparison: %v", err)
	}
	var comparison lmeReplicateComparison
	if err := json.Unmarshal(data, &comparison); err != nil {
		t.Fatalf("decode replicate comparison: %v", err)
	}
	if comparison.SchemaVersion != lmeReplicateComparisonSchemaVersion ||
		comparison.MemoryResponseCacheMode !=
			lmeReplicateMemoryCachesIndependent ||
		comparison.ComparisonScope !=
			lmeReplicateScopeThreeArmPromotion ||
		!comparison.Gate.Passed ||
		!comparison.Gate.IntegrityPassed ||
		!comparison.Gate.OutcomePassed ||
		!comparison.Gate.CostPassed ||
		comparison.ReplicateCount != 3 ||
		len(comparison.Cases) != 2 {
		t.Fatalf("unexpected comparison: %+v", comparison)
	}
	candidate := comparison.Arms[lmeReplicateArmPGVectorCandidate]
	main := comparison.Arms[lmeReplicateArmPGVectorMain]
	mem0 := comparison.Arms[lmeReplicateArmMem0OSS]
	if candidate.MajorityCorrect != 2 || candidate.CorrectReplicates != 6 ||
		main.MajorityCorrect != 1 || main.CorrectReplicates != 2 ||
		mem0.MajorityCorrect != 1 || mem0.CorrectReplicates != 3 {
		t.Fatalf("unexpected arm summaries: main=%+v mem0=%+v candidate=%+v",
			main, mem0, candidate)
	}
	if len(comparison.Pairwise) != 2 {
		t.Fatalf("unexpected pairwise summaries: %+v", comparison.Pairwise)
	}
	for _, pairwise := range comparison.Pairwise {
		if pairwise.InferenceUnit != "question-majority" ||
			pairwise.CandidateCorrect != 2 ||
			pairwise.BaselineCorrect != 1 ||
			pairwise.CandidateWins != 1 ||
			pairwise.BaselineWins != 0 ||
			pairwise.Ties != 1 ||
			pairwise.DiscordantCases != 1 ||
			pairwise.AccuracyDelta != 0.5 ||
			pairwise.ExactMcNemarPValue != 1 {
			t.Fatalf("unexpected pairwise summary: %+v", pairwise)
		}
	}
	if candidate.MemoryTokenUsage.TotalTokens != 240 ||
		candidate.MemoryLogicalTokenUsage.TotalTokens != 240 ||
		!candidate.MemoryLogicalUsageComplete ||
		candidate.AnswerLogicalTokenUsage.TotalTokens != 60 ||
		candidate.JudgeLogicalTokenUsage.TotalTokens != 216 ||
		candidate.MemoryEmbeddingUsage.TotalTokens != 30 ||
		candidate.IngestedPairs != 2 ||
		candidate.FinalMemories != 12 ||
		candidate.ExtractionDiagnostics.TracedPairs != 2 ||
		candidate.ExtractionDiagnostics.ZeroOperationPairs != 2 ||
		candidate.ExtractionDiagnostics.Operations != 0 ||
		candidate.ExtractionDiagnostics.OperationsByStage == nil ||
		candidate.ExtractionDiagnostics.OperationsByType == nil ||
		candidate.ExtractionDiagnostics.PersistenceByStatus == nil ||
		candidate.ExtractionDiagnostics.PersistenceByEffect == nil ||
		mem0.ExtractionDiagnostics.OperationsByStage == nil ||
		mem0.ExtractionDiagnostics.OperationsByType == nil ||
		mem0.ExtractionDiagnostics.PersistenceByStatus == nil ||
		mem0.ExtractionDiagnostics.PersistenceByEffect == nil ||
		candidate.FinalMemoriesByAttribution.Unknown != 12 {
		t.Fatalf("unexpected candidate source cost: %+v", candidate)
	}
	knowledgeType := candidate.ByType["knowledge-update"]
	if knowledgeType == nil ||
		knowledgeType.MemoryLogicalTokenUsage.TotalTokens != 120 ||
		knowledgeType.MemoryEmbeddingUsage.TotalTokens != 15 ||
		knowledgeType.AnswerLogicalTokenUsage.TotalTokens != 30 ||
		knowledgeType.JudgeLogicalTokenUsage.TotalTokens != 108 ||
		knowledgeType.FinalMemories != 6 ||
		knowledgeType.IngestDurationMs != 100 ||
		knowledgeType.SearchDurationMs != 10 {
		t.Fatalf("unexpected candidate type resources: %+v", knowledgeType)
	}
	var knowledgeCase *lmeReplicateCase
	for i := range comparison.Cases {
		if comparison.Cases[i].QuestionID == "q-knowledge" {
			knowledgeCase = &comparison.Cases[i]
			break
		}
	}
	if knowledgeCase == nil {
		t.Fatal("candidate knowledge case is missing")
	}
	knowledgeResources := knowledgeCase.Arms[lmeReplicateArmPGVectorCandidate]
	if knowledgeResources.MemoryLogicalTokenUsage.TotalTokens != 120 ||
		knowledgeResources.MemoryEmbeddingUsage.TotalTokens != 15 ||
		knowledgeResources.AnswerLogicalTokenUsage.TotalTokens != 30 ||
		knowledgeResources.JudgeLogicalTokenUsage.TotalTokens != 108 ||
		knowledgeResources.FinalMemories != 6 ||
		knowledgeResources.IngestDurationMs != 100 ||
		knowledgeResources.SearchDurationMs != 10 {
		t.Fatalf(
			"unexpected candidate case resources: %+v",
			knowledgeResources,
		)
	}
	for _, name := range []string{
		"replicate_comparison.md",
		"replicate_comparison.tsv",
		"replicate_bad_cases.tsv",
	} {
		contents, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		candidateLabel := "pgvector_candidate"
		if name == "replicate_comparison.tsv" {
			candidateLabel = "candidate_majority"
		} else if name == "replicate_bad_cases.tsv" {
			candidateLabel = "earliest_failure_stage"
		}
		if !strings.Contains(string(contents), "q-knowledge") ||
			!strings.Contains(string(contents), candidateLabel) {
			t.Fatalf("%s missing comparison details: %s", name, contents)
		}
		if strings.HasSuffix(name, ".md") {
			for _, section := range []string{
				"## Resource Accounting by Type",
				"## Pairwise Majority Outcomes",
				"## Extraction Diagnostics",
				"## Post-Policy Diagnostics",
				"## Raw Persistence Diagnostics",
				"## Post-Policy Persistence Diagnostics",
				"## Case Failure Attribution",
			} {
				if !strings.Contains(string(contents), section) {
					t.Fatalf("%s missing %s: %s",
						name, section, contents)
				}
			}
		}
	}
	badCases, err := os.ReadFile(
		filepath.Join(outputDir, "replicate_bad_cases.tsv"),
	)
	if err != nil {
		t.Fatalf("read replicate bad cases: %v", err)
	}
	for _, want := range []string{
		"q-knowledge\tknowledge-update\tpgvector_main",
		"q-temporal\ttemporal-reasoning\tpgvector_main",
		"unstable\tevidence_or_answer_miss",
		"stable_incorrect\tevidence_or_answer_miss",
	} {
		if !strings.Contains(string(badCases), want) {
			t.Fatalf("replicate bad cases missing %q: %s", want, badCases)
		}
	}
	if strings.Contains(
		string(badCases),
		"q-knowledge\tknowledge-update\tpgvector_candidate",
	) {
		t.Fatalf("stable healthy candidate should not be a bad case: %s",
			badCases)
	}
}

func TestLongMemEvalReplicateStageSummaryUsesPipelineOrder(t *testing.T) {
	t.Parallel()

	stage, counts := longMemEvalReplicateStageSummary([]string{
		"ok",
		"retrieval_session_miss",
		"extraction_turn_miss",
	})
	if stage != "extraction_turn_miss" ||
		counts != "extraction_turn_miss=1;ok=1;retrieval_session_miss=1" {
		t.Fatalf("stage = %q, counts = %q", stage, counts)
	}

	stage, counts = longMemEvalReplicateStageSummary([]string{
		"custom-z",
		"custom-a",
	})
	if stage != "custom-a" || counts != "custom-a=1;custom-z=1" {
		t.Fatalf("fallback stage = %q, counts = %q", stage, counts)
	}
}

func TestAnalyzeLongMemEvalPairwise(t *testing.T) {
	t.Parallel()

	replicateCase := func(
		id string,
		candidateCorrect bool,
		baselineCorrect bool,
	) lmeReplicateCase {
		return lmeReplicateCase{
			QuestionID: id,
			Arms: map[string]lmeReplicateCaseArmSummary{
				lmeReplicateArmPGVectorCandidate: {
					MajorityCorrect: candidateCorrect,
				},
				lmeReplicateArmPGVectorMain: {
					MajorityCorrect: baselineCorrect,
				},
			},
		}
	}
	cases := []lmeReplicateCase{
		replicateCase("candidate-1", true, false),
		replicateCase("candidate-2", true, false),
		replicateCase("candidate-3", true, false),
		replicateCase("candidate-4", true, false),
		replicateCase("candidate-5", true, false),
		replicateCase("baseline-1", false, true),
		replicateCase("both-correct", true, true),
		replicateCase("both-wrong", false, false),
	}

	got, err := analyzeLongMemEvalPairwise(
		cases,
		lmeReplicateArmPGVectorCandidate,
		lmeReplicateArmPGVectorMain,
	)
	if err != nil {
		t.Fatalf("analyze pairwise outcomes: %v", err)
	}
	if got.Cases != 8 ||
		got.CandidateCorrect != 6 ||
		got.BaselineCorrect != 2 ||
		got.CandidateWins != 5 ||
		got.BaselineWins != 1 ||
		got.Ties != 2 ||
		got.DiscordantCases != 6 ||
		got.AccuracyDelta != 0.5 ||
		got.ExactMcNemarPValue != 0.21875 {
		t.Fatalf("unexpected pairwise analysis: %+v", got)
	}
}

func TestAnalyzeLongMemEvalPairwiseRejectsMissingArm(t *testing.T) {
	t.Parallel()

	_, err := analyzeLongMemEvalPairwise(
		[]lmeReplicateCase{{QuestionID: "missing"}},
		lmeReplicateArmPGVectorCandidate,
		lmeReplicateArmPGVectorMain,
	)
	if err == nil || !strings.Contains(err.Error(), "missing candidate arm") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExactMcNemarPValueNoDiscordantCases(t *testing.T) {
	t.Parallel()

	if got := exactMcNemarPValue(0, 0); got != 1 {
		t.Fatalf("exact McNemar p-value = %v, want 1", got)
	}
}

func TestCompareLongMemEvalIndependentReanswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := writeLongMemEvalReplicateFixture(
		t, dir, lmeReplicateKindIndependentReanswer,
	)
	outputDir := filepath.Join(dir, "output")
	if err := compareLongMemEvalReplicates(manifestPath, outputDir); err != nil {
		t.Fatalf("compare independent reanswers: %v", err)
	}

	data, err := os.ReadFile(
		filepath.Join(outputDir, "replicate_comparison.json"),
	)
	if err != nil {
		t.Fatalf("read replicate comparison: %v", err)
	}
	var comparison lmeReplicateComparison
	if err := json.Unmarshal(data, &comparison); err != nil {
		t.Fatalf("decode replicate comparison: %v", err)
	}
	if comparison.ReplicateCount != 3 ||
		comparison.Arms[lmeReplicateArmPGVectorCandidate].MajorityCorrect != 2 {
		t.Fatalf("unexpected comparison: %+v", comparison)
	}
}

func TestReplicateGateUsesLogicalEmbeddingRequests(t *testing.T) {
	t.Parallel()

	arm := func(name string, majority, correct, requests, providerTokens int) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryTokenUsage:           lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage: lmeEmbeddingUsage{
				Requests: requests, TotalTokens: providerTokens,
			},
			FinalMemories: 10,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: majority},
			},
		}
	}
	comparison := &lmeReplicateComparison{Arms: map[string]*lmeReplicateArm{
		lmeReplicateArmPGVectorMain:      arm(lmeReplicateArmPGVectorMain, 0, 0, 100, 100),
		lmeReplicateArmMem0OSS:           arm(lmeReplicateArmMem0OSS, 0, 0, 100, 100),
		lmeReplicateArmPGVectorCandidate: arm(lmeReplicateArmPGVectorCandidate, 2, 6, 201, 0),
	}}
	gate := lmeReplicatePromotionGate{
		ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 0,
		MemoryLLMTokenRatioMaximum:         1.55,
		MemoryEmbeddingRequestRatioMaximum: 2,
		FinalMemoryCountRatioMaximum:       3,
	}

	result := evaluateLongMemEvalReplicateGate(comparison, gate)
	if result.Passed {
		t.Fatal("gate passed despite excessive logical embedding requests")
	}
	for _, check := range result.Checks {
		if check.Name == "candidate_memory_embedding_requests_vs_main" {
			if check.Passed || check.Actual != "2.010000 (201/100)" {
				t.Fatalf("embedding request check = %+v", check)
			}
			return
		}
	}
	t.Fatal("logical embedding request check is missing")
}

func TestReplicateGateOptionallyUsesEmbeddingTokens(t *testing.T) {
	t.Parallel()

	arm := func(
		name string,
		majority int,
		correct int,
		embeddingTokens int,
	) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage: lmeEmbeddingUsage{
				Requests:    100,
				TotalTokens: embeddingTokens,
			},
			FinalMemories: 10,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: majority},
			},
		}
	}
	comparison := &lmeReplicateComparison{
		Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain: arm(
				lmeReplicateArmPGVectorMain, 0, 0, 100,
			),
			lmeReplicateArmMem0OSS: arm(
				lmeReplicateArmMem0OSS, 0, 0, 100,
			),
			lmeReplicateArmPGVectorCandidate: arm(
				lmeReplicateArmPGVectorCandidate, 2, 6, 250,
			),
		},
	}
	gate := lmeReplicatePromotionGate{
		ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 0,
		MemoryLLMTokenRatioMaximum:         1.55,
		MemoryEmbeddingRequestRatioMaximum: 2,
		FinalMemoryCountRatioMaximum:       3,
	}

	withoutTokenGate := evaluateLongMemEvalReplicateGate(comparison, gate)
	for _, check := range withoutTokenGate.Checks {
		if check.Name == "candidate_memory_embedding_tokens_vs_main" {
			t.Fatalf("optional embedding token check unexpectedly present: %+v",
				check)
		}
	}

	gate.MemoryEmbeddingTokenRatioMaximum = 2
	withTokenGate := evaluateLongMemEvalReplicateGate(comparison, gate)
	for _, check := range withTokenGate.Checks {
		if check.Name != "candidate_memory_embedding_tokens_vs_main" {
			continue
		}
		if check.Passed || check.Actual != "2.500000 (250/100)" {
			t.Fatalf("embedding token check = %+v", check)
		}
		return
	}
	t.Fatal("embedding token check is missing")
}

func TestReplicateGateOptionallyUsesUncachedLogicalTokens(t *testing.T) {
	t.Parallel()

	arm := func(
		name string,
		majority int,
		correct int,
		totalTokens int,
		cachedTokens int,
	) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryLogicalTokenUsage: lmeTokenUsage{
				TotalTokens:  totalTokens,
				CachedTokens: cachedTokens,
			},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage:       lmeEmbeddingUsage{Requests: 100},
			FinalMemories:              10,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: majority},
			},
		}
	}
	comparison := &lmeReplicateComparison{
		Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain: arm(
				lmeReplicateArmPGVectorMain, 0, 0, 100, 80,
			),
			lmeReplicateArmMem0OSS: arm(
				lmeReplicateArmMem0OSS, 0, 0, 100, 80,
			),
			lmeReplicateArmPGVectorCandidate: arm(
				lmeReplicateArmPGVectorCandidate, 2, 6, 120, 20,
			),
		},
	}
	gate := lmeReplicatePromotionGate{
		ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 0,
		MemoryLLMTokenRatioMaximum:         1.55,
		MemoryEmbeddingRequestRatioMaximum: 2,
		FinalMemoryCountRatioMaximum:       3,
	}

	withoutUncachedGate := evaluateLongMemEvalReplicateGate(
		comparison,
		gate,
	)
	for _, check := range withoutUncachedGate.Checks {
		if check.Name == "candidate_memory_llm_uncached_tokens_vs_main" {
			t.Fatalf("optional uncached check unexpectedly present: %+v", check)
		}
	}

	gate.MemoryLLMUncachedTokenRatioMaximum = 2
	withUncachedGate := evaluateLongMemEvalReplicateGate(comparison, gate)
	for _, check := range withUncachedGate.Checks {
		if check.Name != "candidate_memory_llm_uncached_tokens_vs_main" {
			continue
		}
		if check.Passed || check.Actual != "5.000000 (100/20)" {
			t.Fatalf("uncached token check = %+v", check)
		}
		return
	}
	t.Fatal("uncached token check is missing")
}

func TestReplicateGateOptionallyUsesOperationalLatency(t *testing.T) {
	t.Parallel()

	arm := func(
		name string,
		majority int,
		correct int,
		ingestMs int64,
		searchMs int64,
	) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage:       lmeEmbeddingUsage{Requests: 10},
			IngestedPairs:              2,
			FinalMemories:              10,
			IngestDurationMs:           ingestMs,
			SearchDurationMs:           searchMs,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: majority},
			},
		}
	}
	caseArms := func(
		candidateIngestMs int64,
		candidateSearchMs int64,
	) map[string]lmeReplicateCaseArmSummary {
		return map[string]lmeReplicateCaseArmSummary{
			lmeReplicateArmPGVectorMain: {
				IngestedPairs: 1, IngestDurationMs: 500,
				SearchDurationMs: 500,
			},
			lmeReplicateArmMem0OSS: {
				IngestedPairs: 1, IngestDurationMs: 500,
				SearchDurationMs: 500,
			},
			lmeReplicateArmPGVectorCandidate: {
				IngestedPairs: 1, IngestDurationMs: candidateIngestMs,
				SearchDurationMs: candidateSearchMs,
			},
		}
	}
	comparison := &lmeReplicateComparison{
		Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain: arm(
				lmeReplicateArmPGVectorMain, 0, 0, 1000, 1000,
			),
			lmeReplicateArmMem0OSS: arm(
				lmeReplicateArmMem0OSS, 0, 0, 1000, 1000,
			),
			lmeReplicateArmPGVectorCandidate: arm(
				lmeReplicateArmPGVectorCandidate, 2, 6, 1900, 5000,
			),
		},
		Cases: []lmeReplicateCase{
			{Arms: caseArms(900, 1000)},
			{Arms: caseArms(1000, 4000)},
		},
	}
	gate := lmeReplicatePromotionGate{
		ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 0,
		MemoryLLMTokenRatioMaximum:         1.55,
		MemoryEmbeddingRequestRatioMaximum: 2,
		FinalMemoryCountRatioMaximum:       3,
	}

	withoutLatencyGate := evaluateLongMemEvalReplicateGate(
		comparison,
		gate,
	)
	for _, check := range withoutLatencyGate.Checks {
		if strings.Contains(check.Name, "_duration") {
			t.Fatalf("optional latency check unexpectedly present: %+v", check)
		}
	}

	gate.MemoryIngestDurationRatioMaximum = 2
	gate.MemorySearchDurationRatioMaximum = 2
	gate.MemorySearchMinimumAllowanceMs = 36_000
	gate.MemorySearchP95MaximumMs = 5_000
	withLatencyGate := evaluateLongMemEvalReplicateGate(comparison, gate)
	if !withLatencyGate.Passed {
		t.Fatalf("latency gate unexpectedly failed: %+v", withLatencyGate)
	}

	comparison.Cases[1].Arms = caseArms(1_000, 5_001)
	p95Failure := evaluateLongMemEvalReplicateGate(comparison, gate)
	assertLongMemEvalReplicateCheck(
		t,
		p95Failure,
		"candidate_memory_search_duration_p95",
		false,
		"p95=5001ms complete=true cases=2",
	)

	comparison.Cases[1].Arms = caseArms(1_000, 4_000)
	comparison.Arms[lmeReplicateArmPGVectorCandidate].IngestDurationMs = 2_001
	ingestFailure := evaluateLongMemEvalReplicateGate(comparison, gate)
	assertLongMemEvalReplicateCheck(
		t,
		ingestFailure,
		"candidate_memory_ingest_duration_vs_main",
		false,
		"2.001000 (2001ms/1000ms)",
	)

	comparison.Arms[lmeReplicateArmPGVectorCandidate].IngestDurationMs = 1_900
	comparison.Arms[lmeReplicateArmPGVectorCandidate].SearchDurationMs = 36_001
	searchFailure := evaluateLongMemEvalReplicateGate(comparison, gate)
	assertLongMemEvalReplicateCheck(
		t,
		searchFailure,
		"candidate_memory_search_duration_vs_main",
		false,
		"candidate=36001ms main=1000ms allowed=36000ms",
	)

	comparison.Arms[lmeReplicateArmPGVectorCandidate].SearchDurationMs = 5_000
	comparison.Cases[1].Arms = caseArms(0, 4_000)
	integrityFailure := evaluateLongMemEvalReplicateGate(comparison, gate)
	assertLongMemEvalReplicateLatencyIntegrityFailure(t, integrityFailure)

	comparison.Cases[1].Arms = caseArms(1_000, 4_000)
	delete(
		comparison.Cases[1].Arms,
		lmeReplicateArmPGVectorCandidate,
	)
	missingArmFailure := evaluateLongMemEvalReplicateGate(comparison, gate)
	assertLongMemEvalReplicateLatencyIntegrityFailure(t, missingArmFailure)

	comparison.Cases[1].Arms = caseArms(1_000, -1)
	negativeSearchFailure := evaluateLongMemEvalReplicateGate(
		comparison,
		gate,
	)
	assertLongMemEvalReplicateLatencyIntegrityFailure(
		t,
		negativeSearchFailure,
	)
}

func assertLongMemEvalReplicateLatencyIntegrityFailure(
	t *testing.T,
	result lmeReplicateGateResult,
) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name != "latency_duration_completeness" {
			continue
		}
		if check.Passed ||
			check.Dimension != lmeReplicateGateDimensionIntegrity {
			t.Fatalf("latency integrity check = %+v", check)
		}
		return
	}
	t.Fatal("latency integrity check is missing")
}

func assertLongMemEvalReplicateCheck(
	t *testing.T,
	result lmeReplicateGateResult,
	name string,
	passed bool,
	actual string,
) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name != name {
			continue
		}
		if check.Passed != passed || check.Actual != actual ||
			check.Dimension != lmeReplicateGateDimensionCost {
			t.Fatalf("%s check = %+v", name, check)
		}
		return
	}
	t.Fatalf("%s check is missing", name)
}

func TestReplicateGateSeparatesOutcomeFromIntegrityAndCost(t *testing.T) {
	t.Parallel()

	arm := func(name string) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 1, MajorityCorrect: 1,
			CorrectReplicates: 2, ProviderUsageReportedCases: 1,
			MemoryTokenUsage:           lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage:       lmeEmbeddingUsage{Requests: 10},
			IngestedPairs:              1,
			FinalMemories:              5,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: 1},
			},
		}
	}
	caseArms := map[string]lmeReplicateCaseArmSummary{
		lmeReplicateArmPGVectorMain:      {IngestedPairs: 1},
		lmeReplicateArmMem0OSS:           {IngestedPairs: 1},
		lmeReplicateArmPGVectorCandidate: {IngestedPairs: 1},
	}
	comparison := &lmeReplicateComparison{
		Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain: arm(
				lmeReplicateArmPGVectorMain,
			),
			lmeReplicateArmMem0OSS: arm(
				lmeReplicateArmMem0OSS,
			),
			lmeReplicateArmPGVectorCandidate: arm(
				lmeReplicateArmPGVectorCandidate,
			),
		},
		Cases: []lmeReplicateCase{{Arms: caseArms}},
	}
	gate := lmeReplicatePromotionGate{
		ExpectedCases: 1, JudgeRuns: 3, PerTypeMaxDeficit: 0,
		MemoryLLMTokenRatioMaximum:         1.55,
		MemoryEmbeddingRequestRatioMaximum: 2,
		FinalMemoryCountRatioMaximum:       3,
	}
	result := evaluateLongMemEvalReplicateGate(comparison, gate)

	if result.Passed || result.OutcomePassed {
		t.Fatalf("outcome gate unexpectedly passed: %+v", result)
	}
	if !result.IntegrityPassed || !result.CostPassed {
		t.Fatalf("non-outcome gate unexpectedly failed: %+v", result)
	}
	for _, check := range result.Checks {
		if strings.HasPrefix(check.Name, "candidate_majority_vs_") &&
			check.Dimension != lmeReplicateGateDimensionOutcome {
			t.Fatalf("outcome check has wrong dimension: %+v", check)
		}
		if strings.HasSuffix(check.Name, "_ingested_pairs") &&
			check.Dimension != lmeReplicateGateDimensionIntegrity {
			t.Fatalf("integrity check has wrong dimension: %+v", check)
		}
		if check.Name == "candidate_memory_llm_tokens_vs_main" &&
			check.Dimension != lmeReplicateGateDimensionCost {
			t.Fatalf("cost check has wrong dimension: %+v", check)
		}
	}

	comparison.Arms[lmeReplicateArmPGVectorCandidate].
		MemoryEmbeddingUsage.ProviderErrors = 1
	result = evaluateLongMemEvalReplicateGate(comparison, gate)
	if result.IntegrityPassed {
		t.Fatal("integrity gate passed with an embedding provider error")
	}
	for _, check := range result.Checks {
		if check.Name == "pgvector_candidate_provider_usage" {
			if check.Passed ||
				!strings.Contains(check.Actual, "embedding_errors=1") {
				t.Fatalf("provider usage check = %+v", check)
			}
			return
		}
	}
	t.Fatal("candidate provider usage check is missing")
}

func TestReplicateGateRejectsIncompleteLogicalTokenCosts(t *testing.T) {
	t.Parallel()

	arm := func(name string) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: 1,
			CorrectReplicates: 3, ProviderUsageReportedCases: 2,
			MemoryTokenUsage:           lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage:       lmeEmbeddingUsage{Requests: 100},
			FinalMemories:              10,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: 1},
			},
		}
	}
	main := arm(lmeReplicateArmPGVectorMain)
	mem0 := arm(lmeReplicateArmMem0OSS)
	candidate := arm(lmeReplicateArmPGVectorCandidate)
	candidate.MajorityCorrect = 2
	candidate.CorrectReplicates = 6
	candidate.MemoryTokenUsage.TotalTokens = 1
	candidate.MemoryLogicalTokenUsage = lmeTokenUsage{}
	candidate.MemoryLogicalUsageComplete = false
	candidate.MemoryModelRequests = 100
	candidate.MemoryLogicalUsageMissing = 100
	candidate.ModelCacheInitialKnown = true
	candidate.ModelCacheInitialEntries = 200
	candidate.MemoryModelCacheHits = 100

	result := evaluateLongMemEvalReplicateGate(
		&lmeReplicateComparison{Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain:      main,
			lmeReplicateArmMem0OSS:           mem0,
			lmeReplicateArmPGVectorCandidate: candidate,
		}},
		lmeReplicatePromotionGate{
			ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 1,
			MemoryLLMTokenRatioMaximum:         1.55,
			MemoryEmbeddingRequestRatioMaximum: 2,
			FinalMemoryCountRatioMaximum:       3,
		},
	)
	if result.Passed {
		t.Fatal("gate passed despite incomparable token costs")
	}
	for _, check := range result.Checks {
		if check.Name != "candidate_memory_llm_tokens_vs_main" {
			continue
		}
		if check.Passed ||
			!strings.Contains(check.Actual, "candidate missing=100/100") {
			t.Fatalf("token comparability check = %+v", check)
		}
		return
	}
	t.Fatal("token comparability check is missing")
}

func TestReplicateGateRejectsIngestionPairMismatch(t *testing.T) {
	t.Parallel()

	arm := func(
		name string,
		majority, correct, pairs int,
	) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryTokenUsage:           lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalTokenUsage:    lmeTokenUsage{TotalTokens: 100},
			MemoryLogicalUsageComplete: true,
			MemoryEmbeddingUsage:       lmeEmbeddingUsage{Requests: 100},
			IngestedPairs:              pairs,
			FinalMemories:              10,
			ByType: map[string]*lmeReplicateTypeSummary{
				"single-session-user": {MajorityCorrect: majority},
			},
		}
	}
	main := arm(lmeReplicateArmPGVectorMain, 0, 0, 2)
	mem0 := arm(lmeReplicateArmMem0OSS, 0, 0, 2)
	candidate := arm(lmeReplicateArmPGVectorCandidate, 2, 6, 1)
	caseSummary := func(mainPairs, mem0Pairs, candidatePairs int) lmeReplicateCase {
		return lmeReplicateCase{
			QuestionType: "single-session-user",
			Arms: map[string]lmeReplicateCaseArmSummary{
				lmeReplicateArmPGVectorMain: {
					IngestedPairs: mainPairs,
				},
				lmeReplicateArmMem0OSS: {
					IngestedPairs: mem0Pairs,
				},
				lmeReplicateArmPGVectorCandidate: {
					IngestedPairs: candidatePairs,
				},
			},
		}
	}
	comparison := &lmeReplicateComparison{
		Arms: map[string]*lmeReplicateArm{
			lmeReplicateArmPGVectorMain:      main,
			lmeReplicateArmMem0OSS:           mem0,
			lmeReplicateArmPGVectorCandidate: candidate,
		},
		Cases: []lmeReplicateCase{
			caseSummary(1, 1, 1),
			caseSummary(1, 1, 0),
		},
	}
	result := evaluateLongMemEvalReplicateGate(
		comparison,
		lmeReplicatePromotionGate{
			ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 0,
			MemoryLLMTokenRatioMaximum:         1.55,
			MemoryEmbeddingRequestRatioMaximum: 2,
			FinalMemoryCountRatioMaximum:       3,
		},
	)
	if result.Passed {
		t.Fatal("gate passed despite missing candidate ingestion pair")
	}
	for _, check := range result.Checks {
		if check.Name != lmeReplicateArmPGVectorCandidate+
			"_ingested_pairs" {
			continue
		}
		if check.Passed ||
			check.Actual !=
				"total=1 main_total=2 mismatched_cases=1" {
			t.Fatalf("candidate ingestion check = %+v", check)
		}
		return
	}
	t.Fatal("candidate ingestion check is missing")
}

func TestReplicateSourceCostRejectsInconsistentIngestionTraceCount(
	t *testing.T,
) {
	t.Parallel()

	result := longMemEvalReplicateFixtureResult(
		"candidate-2196",
		"answer-ledger",
		"judge-ledger",
		lmeReplicateKindIndependentReanswer,
		map[string][2]bool{"pgvector": {true, true}},
	)
	result.Cases[0].BackendResults["pgvector"].IngestedPairs = 2
	err := addLongMemEvalReplicateSourceCost(
		&lmeReplicateArm{},
		result,
		"pgvector",
	)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"records 2 ingested pairs but has 1 ingestion traces",
		) {
		t.Fatalf("source cost error = %v", err)
	}
}

func TestReplicateSourceCostInfersMem0EmbeddingRequests(t *testing.T) {
	t.Parallel()

	result := longMemEvalReplicateFixtureResult(
		"mem0-oss",
		"answer-ledger",
		"judge-ledger",
		lmeReplicateKindIndependentReanswer,
		map[string][2]bool{"mem0": {true, true}},
	)
	for _, cr := range result.Cases {
		cr.BackendResults["mem0"].EmbeddingUsage.Requests = 0
	}

	arm := &lmeReplicateArm{}
	if err := addLongMemEvalReplicateSourceCost(
		arm, result, "mem0",
	); err != nil {
		t.Fatalf("add source cost: %v", err)
	}
	if arm.MemoryEmbeddingUsage.Calls != 4 ||
		arm.MemoryEmbeddingUsage.Requests != 4 {
		t.Fatalf("embedding usage = %+v", arm.MemoryEmbeddingUsage)
	}
}

func TestLongMemEvalReplicateValidationRejectsDrift(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := writeLongMemEvalReplicateFixture(t, dir)
	manifest, digest, pairs, err := loadLongMemEvalReplicateComparison(manifestPath)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	pairs[1].Candidate.Cases[0].BackendResults["pgvector"].Retrieval[0].Memory = "changed retrieval"
	_, err = aggregateLongMemEvalReplicates(digest, manifest, pairs)
	if err == nil || !strings.Contains(err.Error(), "changed immutable ingestion or retrieval source") {
		t.Fatalf("source drift error = %v", err)
	}
}

func TestLongMemEvalReplicateSourceDigestIgnoresAnswerTimestamps(t *testing.T) {
	t.Parallel()

	result := longMemEvalReplicateFixtureResult(
		"candidate-2196", "answer-ledger", "judge-ledger",
		lmeReplicateKindIndependentReanswer,
		map[string][2]bool{"pgvector": {true, true}},
	)
	result.Metadata["reanswered_at"] = "2026-07-20T12:00:00Z"
	first, err := longMemEvalReplicateSourceDigest(result, "pgvector")
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	result.Metadata["reanswered_at"] = "2026-07-20T13:00:00Z"
	second, err := longMemEvalReplicateSourceDigest(result, "pgvector")
	if err != nil {
		t.Fatalf("second digest: %v", err)
	}
	if first != second {
		t.Fatalf("answer timestamp changed source digest: %s != %s", first, second)
	}
	result.Cases[0].BackendResults["pgvector"].EvaluatedFailureStage = "ok"
	third, err := longMemEvalReplicateSourceDigest(result, "pgvector")
	if err != nil {
		t.Fatalf("evaluated stage digest: %v", err)
	}
	if first != third {
		t.Fatalf("evaluated answer stage changed source digest: %s != %s",
			first, third)
	}
}

func TestLongMemEvalReplicateSourceUsageValidation(t *testing.T) {
	t.Parallel()

	valid := longMemEvalReplicateFixtureBackend("pgvector", "q", true, 100, 10, 5)
	for _, test := range []struct {
		name      string
		mutate    func(*backendResult)
		wantError string
	}{
		{name: "total usage", mutate: func(br *backendResult) { br.TokenUsage = nil }, wantError: "token_usage is missing"},
		{name: "answer usage", mutate: func(br *backendResult) { br.AnswerUsage = nil }, wantError: "answer_token_usage is missing"},
		{name: "embedding usage", mutate: func(br *backendResult) { br.EmbeddingUsage = nil }, wantError: "embedding_usage is missing"},
		{name: "answer exceeds total", mutate: func(br *backendResult) { br.AnswerUsage.TotalTokens = br.TokenUsage.TotalTokens + 1 }, wantError: "answer total_tokens"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copyBR := *valid
			if valid.TokenUsage != nil {
				usage := *valid.TokenUsage
				copyBR.TokenUsage = &usage
			}
			if valid.AnswerUsage != nil {
				usage := *valid.AnswerUsage
				copyBR.AnswerUsage = &usage
			}
			if valid.EmbeddingUsage != nil {
				usage := *valid.EmbeddingUsage
				copyBR.EmbeddingUsage = &usage
			}
			test.mutate(&copyBR)
			_, err := longMemEvalReplicateMemoryLayerUsage(&copyBR)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("usage validation error = %v, want %q", err, test.wantError)
			}
		})
	}
	usage, err := longMemEvalReplicateMemoryLayerUsage(valid)
	if err != nil {
		t.Fatalf("valid usage: %v", err)
	}
	if usage.TotalTokens != 100 || usage.LLMCalls != 1 {
		t.Fatalf("memory-layer usage = %+v", usage)
	}

	cached := *valid
	cachedTotalUsage := *valid.TokenUsage
	cachedTotalUsage.Sub(*valid.AnswerUsage)
	cached.TokenUsage = &cachedTotalUsage
	cached.AnswerUsage = nil
	cached.AnswerSource = lmeAnswerSourcePersistent
	usage, err = longMemEvalReplicateMemoryLayerUsage(&cached)
	if err != nil {
		t.Fatalf("cached answer usage: %v", err)
	}
	if usage != cachedTotalUsage {
		t.Fatalf("cached memory-layer usage = %+v, want %+v", usage, cachedTotalUsage)
	}

	cached.AnswerLogicalUsage = nil
	_, err = longMemEvalReplicateMemoryLayerUsage(&cached)
	if err == nil || !strings.Contains(err.Error(), "answer_logical_token_usage is missing") {
		t.Fatalf("cached logical usage validation error = %v", err)
	}
}

func TestLoadLongMemEvalReplicatesRejectsSharedLedgers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		key, wantError string
	}{
		{key: "answer_cache_ledger_id", wantError: "share answer cache ledger"},
		{key: "judge_cache_ledger_id", wantError: "share judge cache ledger"},
	} {
		t.Run(test.key, func(t *testing.T) {
			dir := t.TempDir()
			manifestPath := writeLongMemEvalReplicateFixture(t, dir)
			manifest, _, pairs, err := loadLongMemEvalReplicateComparison(manifestPath)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			shared := pairs[0].Baseline.Metadata[test.key]
			for _, result := range []*runResult{pairs[1].Baseline, pairs[1].Candidate} {
				result.Metadata[test.key] = shared
			}
			for _, item := range []struct {
				path   string
				result *runResult
			}{
				{path: manifest.Replicates[1].BaselineResults, result: pairs[1].Baseline},
				{path: manifest.Replicates[1].CandidateResults, result: pairs[1].Candidate},
			} {
				if err := writeLongMemEvalResults(filepath.Join(dir, item.path), item.result); err != nil {
					t.Fatalf("rewrite %s: %v", item.path, err)
				}
			}
			_, _, _, err = loadLongMemEvalReplicateComparison(manifestPath)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("shared ledger error = %v", err)
			}
		})
	}
}

func TestValidateLongMemEvalReplicateManifest(t *testing.T) {
	t.Parallel()

	valid := lmeReplicateComparisonManifest{
		SchemaVersion: lmeReplicateManifestSchemaVersion,
		Replicates: []lmeReplicateComparisonPair{
			{Name: "r1", Kind: lmeReplicateKindPrimary, BaselineResults: "b1", CandidateResults: "c1"},
			{Name: "r2", Kind: lmeReplicateKindIndependentReanswer, BaselineResults: "b2", CandidateResults: "c2"},
			{Name: "r3", Kind: lmeReplicateKindIndependentReanswer, BaselineResults: "b3", CandidateResults: "c3"},
		},
		Gate: lmeReplicatePromotionGate{
			ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 1,
			MemoryLLMTokenRatioMaximum:         1.35,
			MemoryEmbeddingRequestRatioMaximum: 2,
			FinalMemoryCountRatioMaximum:       2,
		},
	}
	for _, test := range []struct {
		name      string
		mutate    func(*lmeReplicateComparisonManifest)
		wantError string
	}{
		{name: "schema", mutate: func(m *lmeReplicateComparisonManifest) { m.SchemaVersion++ }, wantError: "schema version"},
		{name: "even count", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates = m.Replicates[:2] }, wantError: "odd count"},
		{name: "unsupported first kind", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates[0].Kind = "unsupported" }, wantError: "want \"primary\" or \"independent-reanswer\""},
		{name: "later primary kind", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates[1].Kind = lmeReplicateKindPrimary }, wantError: "want \"independent-reanswer\""},
		{name: "duplicate name", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates[1].Name = "r1" }, wantError: "duplicated"},
		{name: "cache mode", mutate: func(m *lmeReplicateComparisonManifest) {
			m.MemoryResponseCacheMode = "unsupported"
		}, wantError: "memory response cache mode"},
		{name: "comparison scope", mutate: func(m *lmeReplicateComparisonManifest) {
			m.ComparisonScope = "unsupported"
		}, wantError: "comparison scope"},
		{name: "incomplete cache timeline", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Replicates[0].AnswerCacheTimelineResults = []string{"b1", "c1"}
		}, wantError: "must register both answer and judge cache timelines"},
		{name: "duplicate cache timeline path", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Replicates[0].AnswerCacheTimelineResults =
				[]string{"b1", "b1"}
			m.Replicates[0].JudgeCacheTimelineResults =
				[]string{"c1", "c1"}
		}, wantError: "empty or duplicated"},
		{name: "gate", mutate: func(m *lmeReplicateComparisonManifest) { m.Gate.JudgeRuns = 2 }, wantError: "invalid promotion gate"},
		{name: "negative uncached gate", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Gate.MemoryLLMUncachedTokenRatioMaximum = -1
		}, wantError: "invalid promotion gate"},
		{name: "negative embedding token gate", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Gate.MemoryEmbeddingTokenRatioMaximum = -1
		}, wantError: "invalid promotion gate"},
		{name: "negative ingest latency gate", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Gate.MemoryIngestDurationRatioMaximum = -1
		}, wantError: "invalid promotion gate"},
		{name: "search allowance without ratio", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Gate.MemorySearchMinimumAllowanceMs = 1
		}, wantError: "invalid promotion gate"},
		{name: "negative search p95 gate", mutate: func(m *lmeReplicateComparisonManifest) {
			m.Gate.MemorySearchP95MaximumMs = -1
		}, wantError: "invalid promotion gate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copyManifest := valid
			copyManifest.Replicates = append([]lmeReplicateComparisonPair(nil), valid.Replicates...)
			test.mutate(&copyManifest)
			err := validateLongMemEvalReplicateManifest(copyManifest)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validation error = %v, want %q", err, test.wantError)
			}
		})
	}
	if err := validateLongMemEvalReplicateManifest(valid); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	valid.MemoryResponseCacheMode = lmeReplicateMemoryCachesShared
	valid.ComparisonScope = lmeReplicateScopePairwise
	if err := validateLongMemEvalReplicateManifest(valid); err != nil {
		t.Fatalf("valid pairwise shared-cache manifest: %v", err)
	}
	allIndependent := valid
	allIndependent.Replicates = append(
		[]lmeReplicateComparisonPair(nil),
		valid.Replicates...,
	)
	allIndependent.Replicates[0].Kind =
		lmeReplicateKindIndependentReanswer
	if err := validateLongMemEvalReplicateManifest(allIndependent); err != nil {
		t.Fatalf("all-independent manifest: %v", err)
	}
}

func TestLoadLongMemEvalReplicateComparisonSupportsSharedMemoryCaches(
	t *testing.T,
) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := writeLongMemEvalReplicateFixture(t, dir)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest lmeReplicateComparisonManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	manifest.MemoryResponseCacheMode = lmeReplicateMemoryCachesShared
	manifest.ComparisonScope = lmeReplicateScopePairwise
	for _, spec := range manifest.Replicates {
		baselinePath := filepath.Join(dir, spec.BaselineResults)
		candidatePath := filepath.Join(dir, spec.CandidateResults)
		baseline, err := loadLongMemEvalResults(baselinePath)
		if err != nil {
			t.Fatalf("load baseline: %v", err)
		}
		candidate, err := loadLongMemEvalResults(candidatePath)
		if err != nil {
			t.Fatalf("load candidate: %v", err)
		}
		for _, key := range []string{
			"model_response_cache_ledger_id",
			"embedding_response_cache_ledger_id",
		} {
			candidate.Metadata[key] = baseline.Metadata[key]
		}
		for _, item := range baseline.Cases {
			delete(item.BackendResults, "mem0")
		}
		if err := writeLongMemEvalResults(baselinePath, baseline); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		if err := writeLongMemEvalResults(candidatePath, candidate); err != nil {
			t.Fatalf("write candidate: %v", err)
		}
	}
	data, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	loaded, _, _, err := loadLongMemEvalReplicateComparison(manifestPath)
	if err != nil {
		t.Fatalf("load shared-cache comparison: %v", err)
	}
	if loaded.MemoryResponseCacheMode != lmeReplicateMemoryCachesShared {
		t.Fatalf("cache mode = %q", loaded.MemoryResponseCacheMode)
	}
	outputDir := filepath.Join(dir, "output")
	if err := compareLongMemEvalReplicates(manifestPath, outputDir); err != nil {
		t.Fatalf("compare shared-cache replicates: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(outputDir, "replicate_comparison.json"))
	if err != nil {
		t.Fatalf("read comparison: %v", err)
	}
	var comparison lmeReplicateComparison
	if err := json.Unmarshal(data, &comparison); err != nil {
		t.Fatalf("decode comparison: %v", err)
	}
	if comparison.MemoryResponseCacheMode != lmeReplicateMemoryCachesShared {
		t.Fatalf("comparison cache mode = %q",
			comparison.MemoryResponseCacheMode)
	}
	if comparison.ComparisonScope != lmeReplicateScopePairwise ||
		!comparison.Gate.Passed ||
		len(comparison.Arms) != 2 ||
		comparison.Arms[lmeReplicateArmMem0OSS] != nil {
		t.Fatalf("unexpected pairwise comparison: %+v", comparison)
	}
}

func TestValidateLongMemEvalReplicateKind(t *testing.T) {
	t.Parallel()

	primary := &runResult{Metadata: map[string]any{}}
	independent := &runResult{Metadata: map[string]any{
		"reanswer_model": "answer-model", "reanswer_reuse_source_answers": false,
	}}
	if err := validateLongMemEvalReplicateKind(
		lmeReplicateComparisonPair{Name: "primary", Kind: lmeReplicateKindPrimary},
		primary, primary,
	); err != nil {
		t.Fatalf("primary kind: %v", err)
	}
	if err := validateLongMemEvalReplicateKind(
		lmeReplicateComparisonPair{Name: "independent", Kind: lmeReplicateKindIndependentReanswer},
		independent, independent,
	); err != nil {
		t.Fatalf("independent kind: %v", err)
	}
	independent.Metadata["reanswer_reuse_source_answers"] = true
	err := validateLongMemEvalReplicateKind(
		lmeReplicateComparisonPair{Name: "invalid", Kind: lmeReplicateKindIndependentReanswer},
		independent, independent,
	)
	if err == nil || !strings.Contains(err.Error(), "reanswer_reuse_source_answers=false") {
		t.Fatalf("source reuse error = %v", err)
	}
}

func TestValidateLongMemEvalReplicateFreshCaches(t *testing.T) {
	t.Parallel()

	baseline := &runResult{Metadata: map[string]any{
		"answer_cache_initial_entries": 0,
		"answer_cache_final_entries":   2,
		"judge_cache_initial_entries":  1,
		"judge_cache_final_entries":    3,
	}}
	candidate := &runResult{Metadata: map[string]any{
		"answer_cache_initial_entries": 2,
		"answer_cache_final_entries":   3,
		"judge_cache_initial_entries":  0,
		"judge_cache_final_entries":    1,
	}}
	if err := validateLongMemEvalReplicateFreshCaches(
		"replicate", baseline, candidate,
	); err != nil {
		t.Fatalf("fresh caches: %v", err)
	}
	for _, test := range []struct {
		name      string
		mutate    func(*runResult, *runResult)
		wantError string
	}{
		{
			name: "missing candidate answer final",
			mutate: func(_, candidate *runResult) {
				delete(candidate.Metadata, "answer_cache_final_entries")
			},
			wantError: "candidate metadata \"answer_cache_final_entries\"",
		},
		{
			name: "nonmonotonic baseline answer cache",
			mutate: func(baseline, _ *runResult) {
				baseline.Metadata["answer_cache_final_entries"] = -1
			},
			wantError: "baseline answer_cache cache entries are not monotonic",
		},
		{
			name: "nonempty start",
			mutate: func(baseline, candidate *runResult) {
				baseline.Metadata["answer_cache_initial_entries"] = 1
				candidate.Metadata["answer_cache_initial_entries"] = 2
			},
			wantError: "answer_cache cache does not form a fresh contiguous timeline",
		},
		{
			name: "timeline gap",
			mutate: func(baseline, _ *runResult) {
				baseline.Metadata["answer_cache_final_entries"] = 1
			},
			wantError: "answer_cache cache does not form a fresh contiguous timeline",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := &runResult{Metadata: maps.Clone(baseline.Metadata)}
			candidate := &runResult{Metadata: maps.Clone(candidate.Metadata)}
			test.mutate(baseline, candidate)
			err := validateLongMemEvalReplicateFreshCaches(
				"replicate", baseline, candidate,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("fresh cache error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateLongMemEvalReplicateRegisteredCacheTimelines(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := []string{"a.json", "b.json", "c.json"}
	for index, path := range paths {
		result := longMemEvalReplicateFixtureResult(
			"variant-"+path,
			"answer-ledger",
			"judge-ledger",
			lmeReplicateKindIndependentReanswer,
			map[string][2]bool{"pgvector": {true, true}},
		)
		result.Metadata["answer_cache_initial_entries"] = index
		result.Metadata["answer_cache_final_entries"] = index + 1
		result.Metadata["judge_cache_initial_entries"] = len(paths) - index - 1
		result.Metadata["judge_cache_final_entries"] = len(paths) - index
		if err := writeLongMemEvalResults(
			filepath.Join(dir, path), result,
		); err != nil {
			t.Fatalf("write timeline result: %v", err)
		}
	}
	spec := lmeReplicateComparisonPair{
		Name:                       "replicate",
		BaselineResults:            paths[0],
		CandidateResults:           paths[2],
		AnswerCacheTimelineResults: paths,
		JudgeCacheTimelineResults:  []string{paths[2], paths[1], paths[0]},
	}
	if err := validateLongMemEvalReplicateRegisteredCacheTimelines(
		dir, spec,
	); err != nil {
		t.Fatalf("registered cache timelines: %v", err)
	}

	middle, err := loadLongMemEvalResults(filepath.Join(dir, paths[1]))
	if err != nil {
		t.Fatalf("load middle result: %v", err)
	}
	middle.Metadata["answer_cache_initial_entries"] = 0
	if err := writeLongMemEvalResults(
		filepath.Join(dir, paths[1]), middle,
	); err != nil {
		t.Fatalf("rewrite middle result: %v", err)
	}
	err = validateLongMemEvalReplicateRegisteredCacheTimelines(dir, spec)
	if err == nil || !strings.Contains(err.Error(), "not fresh and contiguous") {
		t.Fatalf("timeline gap error = %v", err)
	}

	middle.Metadata["answer_cache_initial_entries"] = 1
	if err := writeLongMemEvalResults(
		filepath.Join(dir, paths[1]), middle,
	); err != nil {
		t.Fatalf("restore middle result: %v", err)
	}
	spec.AnswerCacheTimelineResults = paths[:2]
	err = validateLongMemEvalReplicateRegisteredCacheTimelines(dir, spec)
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("missing endpoint error = %v", err)
	}
}

func TestValidateLongMemEvalReplicateComparisonRequiresIndependentFreshMemoryCaches(
	t *testing.T,
) {
	t.Parallel()

	newPair := func() (*runResult, *runResult) {
		baseline := longMemEvalReplicateFixtureResult(
			"upstream-main",
			"answer-ledger",
			"judge-ledger",
			lmeReplicateKindIndependentReanswer,
			map[string][2]bool{
				"pgvector": {true, true},
				"mem0":     {true, true},
			},
		)
		candidate := longMemEvalReplicateFixtureResult(
			"candidate-2196",
			"answer-ledger",
			"judge-ledger",
			lmeReplicateKindIndependentReanswer,
			map[string][2]bool{"pgvector": {true, true}},
		)
		return baseline, candidate
	}

	baseline, candidate := newPair()
	if err := validateLongMemEvalReplicateComparison(
		baseline, candidate,
	); err != nil {
		t.Fatalf("valid independent memory caches: %v", err)
	}

	for _, test := range []struct {
		name      string
		mutate    func(*runResult, *runResult)
		wantError string
	}{
		{
			name: "shared model ledger",
			mutate: func(baseline, candidate *runResult) {
				candidate.Metadata["model_response_cache_ledger_id"] =
					baseline.Metadata["model_response_cache_ledger_id"]
			},
			wantError: "independent model response cache ledgers",
		},
		{
			name: "shared embedding ledger",
			mutate: func(baseline, candidate *runResult) {
				candidate.Metadata["embedding_response_cache_ledger_id"] =
					baseline.Metadata["embedding_response_cache_ledger_id"]
			},
			wantError: "independent embedding response cache ledgers",
		},
		{
			name: "nonempty candidate model ledger",
			mutate: func(_, candidate *runResult) {
				candidate.Metadata["model_response_cache_initial_entries"] = 1
			},
			wantError: "candidate model_response_cache_initial_entries to be 0",
		},
		{
			name: "missing baseline embedding ledger",
			mutate: func(baseline, _ *runResult) {
				delete(
					baseline.Metadata,
					"embedding_response_cache_ledger_id",
				)
			},
			wantError: "embedding_response_cache_ledger_id",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline, candidate := newPair()
			test.mutate(baseline, candidate)
			err := validateLongMemEvalReplicateComparison(
				baseline, candidate,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validation error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateLongMemEvalReplicateLogicalUsage(t *testing.T) {
	t.Parallel()

	newResult := func() *runResult {
		return longMemEvalReplicateFixtureResult(
			"candidate-2196",
			"answer-ledger",
			"judge-ledger",
			lmeReplicateKindIndependentReanswer,
			map[string][2]bool{"pgvector": {true, true}},
		)
	}
	if err := validateLongMemEvalReplicateLogicalUsage(
		"replicate", newResult(),
	); err != nil {
		t.Fatalf("valid logical usage: %v", err)
	}
	for _, test := range []struct {
		name      string
		mutate    func(*runResult)
		wantError string
	}{
		{
			name: "missing cache accounting",
			mutate: func(result *runResult) {
				delete(
					result.Metadata,
					"answer_cache_logical_usage_missing_hits",
				)
			},
			wantError: "must be integer zero",
		},
		{
			name: "missing answer usage",
			mutate: func(result *runResult) {
				result.Cases[0].BackendResults["pgvector"].
					AnswerLogicalUsage = nil
			},
			wantError: "incomplete answer logical usage",
		},
		{
			name: "missing judge usage",
			mutate: func(result *runResult) {
				result.Cases[0].BackendResults["pgvector"].Judge.
					LogicalTokenUsage = nil
			},
			wantError: "incomplete judge logical usage",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := newResult()
			test.mutate(result)
			err := validateLongMemEvalReplicateLogicalUsage(
				"replicate", result,
			)
			if err == nil ||
				!strings.Contains(err.Error(), test.wantError) {
				t.Fatalf(
					"logical usage error = %v, want %q",
					err, test.wantError,
				)
			}
		})
	}
}

func TestReplicateSourceCostRetainsCachedLogicalUsage(t *testing.T) {
	t.Parallel()

	result := longMemEvalReplicateFixtureResult(
		"candidate-2196",
		"answer-ledger",
		"judge-ledger",
		lmeReplicateKindIndependentReanswer,
		map[string][2]bool{"pgvector": {true, true}},
	)
	for _, cr := range result.Cases {
		br := cr.BackendResults["pgvector"]
		call := &br.IngestTraces[0].Extraction.ModelCalls[0]
		call.Source = lmeModelCallSourcePersistent
		br.TokenUsage = &lmeTokenUsage{
			PromptTokens:     br.AnswerUsage.PromptTokens,
			CompletionTokens: br.AnswerUsage.CompletionTokens,
			TotalTokens:      br.AnswerUsage.TotalTokens,
			LLMCalls:         br.AnswerUsage.LLMCalls,
		}
	}

	arm := &lmeReplicateArm{}
	if err := addLongMemEvalReplicateSourceCost(
		arm, result, "pgvector",
	); err != nil {
		t.Fatalf("add source cost: %v", err)
	}
	if arm.MemoryTokenUsage.TotalTokens != 0 ||
		arm.MemoryLogicalTokenUsage.TotalTokens != 240 ||
		arm.MemoryModelRequests != 2 ||
		arm.MemoryModelCacheHits != 2 ||
		!arm.MemoryLogicalUsageComplete ||
		arm.MemoryLogicalUsageMissing != 0 {
		t.Fatalf("cached source cost = %+v", arm)
	}
}

func TestAddLongMemEvalReplicateTraceDiagnostics(t *testing.T) {
	t.Parallel()

	diagnostics := lmeReplicateExtractionDiagnostics{
		OperationsByStage:             make(map[string]int),
		OperationsByType:              make(map[string]int),
		PostPolicyOperationsByStage:   make(map[string]int),
		PostPolicyOperationsByType:    make(map[string]int),
		PostPolicyPersistenceByStatus: make(map[string]int),
		PostPolicyPersistenceByEffect: make(map[string]int),
	}
	addLongMemEvalReplicateTraceDiagnostics(
		&diagnostics,
		ingestTrace{
			NewMemories: []memorySnapshot{
				{AttributedTo: lmeAttributionUser},
				{AttributedTo: lmeAttributionAssistant},
				{},
			},
			Extraction: &extractionTrace{
				Operations: []extractionOperation{
					{
						Stage: "primary",
						Type:  extractor.OperationAdd,
					},
					{
						Stage: "primary",
						Type:  extractor.OperationUpdate,
					},
					{
						Stage: "assistant_result",
						Type:  extractor.OperationAdd,
					},
				},
				Persistence: []extractionPersistenceTrace{
					{
						Status: lmePersistenceObserved,
						Effect: string(extractor.OperationAdd),
					},
					{Status: lmePersistenceAlreadySatisfied},
					{Status: lmePersistenceNotObserved},
				},
				PostPolicyObserved: true,
				PostPolicyOperations: []extractionOperation{
					{
						Stage:    "primary",
						Type:     extractor.OperationUpdate,
						MemoryID: "existing",
					},
					{
						Stage: "assistant_result",
						Type:  extractor.OperationAdd,
					},
				},
				PostPolicyPersistence: []extractionPersistenceTrace{
					{
						Status: lmePersistenceObserved,
						Effect: string(extractor.OperationUpdate),
					},
					{
						Status: lmePersistenceObserved,
						Effect: string(extractor.OperationAdd),
					},
				},
				ModelCalls: []lmeModelCallTrace{{}, {}, {}},
			},
		},
	)
	addLongMemEvalReplicateTraceDiagnostics(
		&diagnostics,
		ingestTrace{NewMemories: []memorySnapshot{{}}},
	)

	if diagnostics.TracedPairs != 1 ||
		diagnostics.OperationPairs != 1 ||
		diagnostics.ZeroOperationPairs != 0 ||
		diagnostics.Operations != 3 ||
		diagnostics.OperationsByStage["primary"] != 2 ||
		diagnostics.OperationsByStage["assistant_result"] != 1 ||
		diagnostics.OperationsByType[string(extractor.OperationAdd)] != 2 ||
		diagnostics.OperationsByType[string(extractor.OperationUpdate)] != 1 ||
		diagnostics.PostPolicyObservedPairs != 1 ||
		diagnostics.PostPolicyOperationPairs != 1 ||
		diagnostics.PostPolicyZeroOperationPairs != 0 ||
		diagnostics.PostPolicyOperations != 2 ||
		diagnostics.PostPolicyOperationsByStage["primary"] != 1 ||
		diagnostics.PostPolicyOperationsByStage["assistant_result"] != 1 ||
		diagnostics.PostPolicyOperationsByType[string(extractor.OperationAdd)] != 1 ||
		diagnostics.PostPolicyOperationsByType[string(extractor.OperationUpdate)] != 1 ||
		diagnostics.MultiCallPairs != 1 ||
		diagnostics.AdditionalModelRequests != 2 ||
		diagnostics.PersistenceTracedOperations != 3 ||
		diagnostics.PersistenceByStatus[lmePersistenceObserved] != 1 ||
		diagnostics.PersistenceByStatus[lmePersistenceAlreadySatisfied] != 1 ||
		diagnostics.PersistenceByStatus[lmePersistenceNotObserved] != 1 ||
		diagnostics.PersistenceByEffect[string(extractor.OperationAdd)] != 1 ||
		diagnostics.PostPolicyPersistenceTraced != 2 ||
		diagnostics.PostPolicyPersistenceByStatus[lmePersistenceObserved] != 2 ||
		diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationAdd)] != 1 ||
		diagnostics.PostPolicyPersistenceByEffect[string(extractor.OperationUpdate)] != 1 ||
		diagnostics.PersistedNewMemoriesByAttribution.User != 1 ||
		diagnostics.PersistedNewMemoriesByAttribution.Assistant != 1 ||
		diagnostics.PersistedNewMemoriesByAttribution.Unknown != 2 {
		t.Fatalf("unexpected extraction diagnostics: %+v", diagnostics)
	}
}

func writeLongMemEvalReplicateFixture(
	t *testing.T,
	dir string,
	firstKind ...string,
) string {
	t.Helper()

	initialKind := lmeReplicateKindPrimary
	if len(firstKind) > 0 {
		initialKind = firstKind[0]
	}
	correctness := []struct {
		main      [2]bool
		mem0      [2]bool
		candidate [2]bool
	}{
		{main: [2]bool{false, true}, mem0: [2]bool{true, false}, candidate: [2]bool{true, true}},
		{main: [2]bool{false, true}, mem0: [2]bool{true, false}, candidate: [2]bool{true, true}},
		{main: [2]bool{false, false}, mem0: [2]bool{true, false}, candidate: [2]bool{true, true}},
	}
	manifest := lmeReplicateComparisonManifest{
		SchemaVersion: lmeReplicateManifestSchemaVersion,
		Gate: lmeReplicatePromotionGate{
			ExpectedCases: 2, JudgeRuns: 3, PerTypeMaxDeficit: 1,
			MemoryLLMTokenRatioMaximum:         1.35,
			MemoryEmbeddingRequestRatioMaximum: 2,
			FinalMemoryCountRatioMaximum:       2,
		},
	}
	for index, scores := range correctness {
		name := "replicate-" + string(rune('1'+index))
		kind := lmeReplicateKindIndependentReanswer
		if index == 0 {
			kind = initialKind
		}
		replicateDir := filepath.Join(dir, name)
		if err := os.MkdirAll(replicateDir, 0755); err != nil {
			t.Fatalf("create replicate dir: %v", err)
		}
		ledger := "answer-ledger-" + name
		judgeLedger := "judge-ledger-" + name
		baseline := longMemEvalReplicateFixtureResult(
			"upstream-main", ledger, judgeLedger, kind,
			map[string][2]bool{"pgvector": scores.main, "mem0": scores.mem0},
		)
		candidate := longMemEvalReplicateFixtureResult(
			"candidate-2196", ledger, judgeLedger, kind,
			map[string][2]bool{"pgvector": scores.candidate},
		)
		baselinePath := filepath.Join(replicateDir, "baseline.json")
		candidatePath := filepath.Join(replicateDir, "candidate.json")
		if err := writeLongMemEvalResults(baselinePath, baseline); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		if err := writeLongMemEvalResults(candidatePath, candidate); err != nil {
			t.Fatalf("write candidate: %v", err)
		}
		manifest.Replicates = append(manifest.Replicates, lmeReplicateComparisonPair{
			Name: name, Kind: kind,
			BaselineResults:  filepath.ToSlash(filepath.Join(name, "baseline.json")),
			CandidateResults: filepath.ToSlash(filepath.Join(name, "candidate.json")),
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	manifestPath := filepath.Join(dir, "replicates.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return manifestPath
}

func longMemEvalReplicateFixtureResult(
	implementation, answerLedger, judgeLedger, kind string,
	backendScores map[string][2]bool,
) *runResult {
	metadata := testLongMemEvalComparisonMetadata(implementation)
	metadata["answer_cache_format_version"] = lmeAnswerCacheFormatVersion
	metadata["answer_cache_shared"] = true
	metadata["answer_cache_ledger_id"] = answerLedger
	metadata["answer_cache_initial_entries"] = 0
	metadata["answer_cache_final_entries"] = 0
	metadata["answer_cache_logical_usage_missing_hits"] = 0
	metadata["judge_cache_ledger_id"] = judgeLedger
	metadata["judge_cache_initial_entries"] = 0
	metadata["judge_cache_final_entries"] = 0
	metadata["judge_cache_logical_usage_missing_hits"] = 0
	metadata["model_response_cache_format_version"] =
		lmeModelCacheFormatVersion
	metadata["model_response_cache_shared"] = true
	metadata["model_response_cache_ledger_id"] =
		"model-ledger-" + implementation
	metadata["model_response_cache_initial_entries"] = 0
	metadata["model_response_cache_hits"] = 0
	metadata["model_response_cache_errors"] = 0
	metadata["embedding_response_cache_format_version"] =
		lmeEmbeddingCacheFormatVersion
	metadata["embedding_response_cache_shared"] = true
	metadata["embedding_response_cache_ledger_id"] =
		"embedding-ledger-" + implementation
	metadata["embedding_response_cache_initial_entries"] = 0
	metadata["embedding_response_cache_errors"] = 0
	metadata["user_scope"] = "replicate-fixture"
	metadata["user_scope_explicit"] = true
	if kind == lmeReplicateKindIndependentReanswer {
		metadata["reanswer_model"] = "answer-model"
		metadata["reanswer_model_variant"] = "glm"
		metadata["reanswer_build"] = testLongMemEvalBuildProvenance("reanswer-revision")
		metadata["reanswer_reuse_source_answers"] = false
	}
	questions := []struct {
		id, typ, question, answer string
	}{
		{id: "q-knowledge", typ: "knowledge-update", question: "Where?", answer: "The suburbs"},
		{id: "q-temporal", typ: "temporal-reasoning", question: "When?", answer: "June 3"},
	}
	result := &runResult{Metadata: metadata}
	for questionIndex, question := range questions {
		cr := &caseResult{
			QuestionID: question.id, QuestionType: question.typ,
			Question: question.question, Answer: question.answer, NumSessions: 1,
			BackendResults: make(map[string]*backendResult),
		}
		for backend, scores := range backendScores {
			memoryTokens, embeddingTokens, memoryCount := 100, 10, 5
			if implementation == "candidate-2196" {
				memoryTokens, embeddingTokens, memoryCount = 120, 15, 6
			} else if backend == "mem0" {
				memoryTokens, embeddingTokens, memoryCount = 110, 12, 6
			}
			cr.BackendResults[backend] = longMemEvalReplicateFixtureBackend(
				backend, question.id, scores[questionIndex],
				memoryTokens, embeddingTokens, memoryCount,
			)
		}
		result.Cases = append(result.Cases, cr)
	}
	return result
}

func longMemEvalReplicateFixtureBackend(
	backend, questionID string,
	correct bool,
	memoryTokens, embeddingTokens, memoryCount int,
) *backendResult {
	answer := "correct"
	stage := "ok"
	if !correct {
		answer = "I don't know"
		stage = "evidence_or_answer_miss"
	}
	memories := make([]memorySnapshot, memoryCount)
	for index := range memories {
		memories[index] = memorySnapshot{ID: questionID + "-memory-" + string(rune('a'+index)), Memory: "stable memory"}
	}
	answerUsage := &lmeTokenUsage{PromptTokens: 8, CompletionTokens: 2, TotalTokens: 10, LLMCalls: 1}
	answerLogicalUsage := *answerUsage
	answerCall := lmeModelCallTrace{
		Source:            lmeModelCallSourceModel,
		LogicalTokenUsage: &answerLogicalUsage,
	}
	answerAttempts := []lmeAnswerAttempt{{
		Raw:                  answer,
		Source:               lmeAnswerSourceModel,
		ModelCalls:           []lmeModelCallTrace{answerCall},
		TokenUsage:           answerUsage,
		LogicalTokenUsage:    &answerLogicalUsage,
		LogicalUsageComplete: true,
	}}
	totalUsage := lmeTokenUsage{PromptTokens: memoryTokens + 8, CompletionTokens: 2, TotalTokens: memoryTokens + 10, LLMCalls: 2}
	judgeRaw := "VERDICT: no"
	if correct {
		judgeRaw = "VERDICT: yes"
	}
	attempts := make([]lmeJudgeAttempt, 3)
	judgeLogicalUsage := lmeTokenUsage{}
	for index := range attempts {
		attemptUsage := lmeTokenUsage{
			PromptTokens: 10, CompletionTokens: 2,
			TotalTokens: 12, LLMCalls: 1,
		}
		judgeLogicalUsage.Add(attemptUsage)
		attempts[index] = lmeJudgeAttempt{
			Correct: correct,
			Raw:     judgeRaw,
			ModelCalls: []lmeModelCallTrace{{
				Source:            lmeModelCallSourceModel,
				LogicalTokenUsage: &attemptUsage,
			}},
			TokenUsage:           &attemptUsage,
			LogicalTokenUsage:    &attemptUsage,
			LogicalUsageComplete: true,
		}
	}
	return &backendResult{
		Backend: backend, UserID: backend + "-" + questionID, SessionID: "session-" + questionID,
		IngestedPairs: 1,
		IngestTraces: []ingestTrace{{
			Extraction: &extractionTrace{
				ModelCalls: []lmeModelCallTrace{{
					Source: lmeModelCallSourceModel,
					LogicalTokenUsage: &lmeTokenUsage{
						PromptTokens: memoryTokens,
						TotalTokens:  memoryTokens,
						LLMCalls:     1,
					},
				}},
			},
		}},
		FinalMemories: memories,
		Retrieval:     []memoryHit{{ID: questionID + "-hit", Memory: "stable answer evidence", Score: 0.9}},
		Answer:        answer, RawAnswer: answer, AnswerSource: lmeAnswerSourceModel,
		AnswerAttempts:     answerAttempts,
		AnswerModelCalls:   []lmeModelCallTrace{answerCall},
		TokenUsage:         &totalUsage,
		AnswerUsage:        answerUsage,
		AnswerLogicalUsage: &answerLogicalUsage,
		EmbeddingUsage: &lmeEmbeddingUsage{
			PromptTokens: embeddingTokens, TotalTokens: embeddingTokens,
			Calls: 2, Requests: 2,
		},
		ProviderUsageReported: true,
		FailureStage:          stage, ExactMatch: correct,
		Judge: &lmeJudgeResult{
			Model: "judge-model", Correct: correct, Raw: judgeRaw,
			RequestedRuns: 3, ValidRuns: 3, Attempts: attempts,
			LogicalTokenUsage:    &judgeLogicalUsage,
			LogicalUsageComplete: true,
		},
		IngestDuration: 100, SearchDuration: 10,
	}
}
