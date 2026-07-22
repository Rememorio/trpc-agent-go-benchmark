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
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !comparison.Gate.Passed || comparison.ReplicateCount != 3 || len(comparison.Cases) != 2 {
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
	if candidate.MemoryTokenUsage.TotalTokens != 240 ||
		candidate.MemoryEmbeddingUsage.TotalTokens != 30 ||
		candidate.FinalMemories != 12 {
		t.Fatalf("unexpected candidate source cost: %+v", candidate)
	}
	for _, name := range []string{"replicate_comparison.md", "replicate_comparison.tsv"} {
		contents, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		candidateLabel := "pgvector_candidate"
		if strings.HasSuffix(name, ".tsv") {
			candidateLabel = "candidate_majority"
		}
		if !strings.Contains(string(contents), "q-knowledge") ||
			!strings.Contains(string(contents), candidateLabel) {
			t.Fatalf("%s missing comparison details: %s", name, contents)
		}
	}
}

func TestReplicateGateUsesLogicalEmbeddingRequests(t *testing.T) {
	t.Parallel()

	arm := func(name string, majority, correct, requests, providerTokens int) *lmeReplicateArm {
		return &lmeReplicateArm{
			Name: name, Cases: 2, MajorityCorrect: majority,
			CorrectReplicates: correct, ProviderUsageReportedCases: 2,
			MemoryTokenUsage: lmeTokenUsage{TotalTokens: 100},
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
		SchemaVersion: lmeReplicateComparisonSchemaVersion,
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
		{name: "primary kind", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates[0].Kind = lmeReplicateKindIndependentReanswer }, wantError: "want \"primary\""},
		{name: "duplicate name", mutate: func(m *lmeReplicateComparisonManifest) { m.Replicates[1].Name = "r1" }, wantError: "duplicated"},
		{name: "gate", mutate: func(m *lmeReplicateComparisonManifest) { m.Gate.JudgeRuns = 2 }, wantError: "invalid promotion gate"},
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

	valid := &runResult{Metadata: map[string]any{
		"answer_cache_initial_entries": 0,
		"judge_cache_initial_entries":  0,
	}}
	if err := validateLongMemEvalReplicateFreshCaches("replicate", valid); err != nil {
		t.Fatalf("fresh caches: %v", err)
	}
	for _, key := range []string{"answer_cache_initial_entries", "judge_cache_initial_entries"} {
		t.Run(key, func(t *testing.T) {
			result := &runResult{Metadata: map[string]any{
				"answer_cache_initial_entries": 0,
				"judge_cache_initial_entries":  0,
			}}
			result.Metadata[key] = 1
			err := validateLongMemEvalReplicateFreshCaches("replicate", result)
			if err == nil || !strings.Contains(err.Error(), key+" is 1, want 0") {
				t.Fatalf("fresh cache error = %v", err)
			}
		})
	}
}

func writeLongMemEvalReplicateFixture(t *testing.T, dir string) string {
	t.Helper()

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
		SchemaVersion: lmeReplicateComparisonSchemaVersion,
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
			kind = lmeReplicateKindPrimary
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
	metadata["judge_cache_ledger_id"] = judgeLedger
	metadata["judge_cache_initial_entries"] = 0
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
	totalUsage := lmeTokenUsage{PromptTokens: memoryTokens + 8, CompletionTokens: 2, TotalTokens: memoryTokens + 10, LLMCalls: 2}
	judgeRaw := "VERDICT: no"
	if correct {
		judgeRaw = "VERDICT: yes"
	}
	attempts := make([]lmeJudgeAttempt, 3)
	for index := range attempts {
		attempts[index] = lmeJudgeAttempt{Correct: correct, Raw: judgeRaw}
	}
	return &backendResult{
		Backend: backend, UserID: backend + "-" + questionID, SessionID: "session-" + questionID,
		IngestedPairs: 1,
		FinalMemories: memories,
		Retrieval:     []memoryHit{{ID: questionID + "-hit", Memory: "stable answer evidence", Score: 0.9}},
		Answer:        answer, RawAnswer: answer, AnswerSource: lmeAnswerSourceModel,
		TokenUsage: &totalUsage, AnswerUsage: answerUsage,
		EmbeddingUsage: &lmeEmbeddingUsage{
			PromptTokens: embeddingTokens, TotalTokens: embeddingTokens,
			Calls: 2, Requests: 2,
		},
		ProviderUsageReported: true,
		FailureStage:          stage, ExactMatch: correct,
		Judge: &lmeJudgeResult{
			Model: "judge-model", Correct: correct, Raw: judgeRaw,
			RequestedRuns: 3, ValidRuns: 3, Attempts: attempts,
		},
		IngestDuration: 100, SearchDuration: 10,
	}
}
