//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRerankLongMemEvalResultAllBackends(t *testing.T) {
	restoreIntFlag(t, flagLMERerankTopN, 1)
	restoreIntFlag(t, flagLMERerankRuns, 1)
	relevant := memoryHit{
		ID:              "relevant",
		Memory:          "Visited the Science Museum.",
		SourceSessions:  []string{"answer-session"},
		SourceHasAnswer: true,
	}
	irrelevant := memoryHit{
		ID:     "irrelevant",
		Memory: "Assistant recommended a database course.",
	}
	result := &runResult{
		Metadata: map[string]any{
			"judge_model": "stale-judge",
			"top_k":       30,
		},
		Cases: []*caseResult{{
			QuestionID:       "q1",
			QuestionType:     "single-session-user",
			Question:         "Which museum did I visit?",
			Answer:           "Science Museum",
			AnswerSessionIDs: []string{"answer-session"},
			BackendResults: map[string]*backendResult{
				"pgvector": rerankTestBackend([]memoryHit{relevant, irrelevant}),
				"mem0":     rerankTestBackend([]memoryHit{irrelevant, relevant}),
			},
		}},
	}
	stop := "stop"
	response := func(content string, prompt, completion int) *model.Response {
		return &model.Response{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage(content),
				FinishReason: &stop,
			}},
			Usage: &model.Usage{
				PromptTokens:     prompt,
				CompletionTokens: completion,
				TotalTokens:      prompt + completion,
			},
		}
	}
	llm := &queuedAnswerModel{responses: []*model.Response{
		response(`{"indices":[2]}`, 8, 2),
		response("Science Museum", 4, 1),
		response(`{"indices":[1]}`, 8, 2),
		response("Science Museum", 4, 1),
	}}
	outPath := t.TempDir() + "/reranked_results.json"
	if err := rerankLongMemEvalResult(
		context.Background(), result, llm,
		"answer-model", "glm", "source-digest", outPath,
	); err != nil {
		t.Fatalf("rerank results: %v", err)
	}
	if len(llm.requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(llm.requests))
	}
	for _, backendName := range []string{"mem0", "pgvector"} {
		br := result.Cases[0].BackendResults[backendName]
		if len(br.PreRerankRetrieval) != 2 || len(br.Retrieval) != 1 ||
			br.Retrieval[0].ID != relevant.ID {
			t.Fatalf("%s reranked retrieval = pre %#v post %#v",
				backendName, br.PreRerankRetrieval, br.Retrieval)
		}
		if br.Answer != "Science Museum" || !br.ExactMatch || br.Judge != nil {
			t.Fatalf("%s reranked answer = %+v", backendName, br)
		}
		if br.RerankUsage == nil || br.RerankUsage.TotalTokens != 10 ||
			br.RerankUsage.LLMCalls != 1 {
			t.Fatalf("%s rerank usage = %#v", backendName, br.RerankUsage)
		}
		if br.AnswerUsage == nil || br.AnswerUsage.TotalTokens != 5 ||
			br.TokenUsage == nil || br.TokenUsage.TotalTokens != 95 {
			t.Fatalf("%s combined usage = total %#v answer %#v",
				backendName, br.TokenUsage, br.AnswerUsage)
		}
	}
	for _, request := range []int{1, 3} {
		if strings.Contains(llm.requests[request].Messages[0].Content, irrelevant.Memory) {
			t.Fatalf("answer request included filtered memory: %s",
				llm.requests[request].Messages[0].Content)
		}
	}
	metadata, ok := result.Metadata["retrieval_rerank"].(map[string]any)
	if !ok || metadata["source_sha256"] != "source-digest" ||
		metadata["prompt_version"] != lmeRerankPromptVersion ||
		metadata["runs"] != 1 ||
		metadata["backend_scope"] != "all saved backend retrieval hits" ||
		metadata["completed_backends"] != 2 {
		t.Fatalf("rerank metadata = %#v", result.Metadata["retrieval_rerank"])
	}
	if result.Metadata["answer_prompt_version"] != lmeAnswerPromptVersion {
		t.Fatalf("answer prompt provenance = %#v", result.Metadata)
	}
	if result.Metadata["rerank_model"] != "answer-model" ||
		result.Metadata["rerank_model_variant"] != "glm" ||
		result.Metadata["rerank_prompt_version"] != lmeRerankPromptVersion ||
		result.Metadata["rerank_top_n"] != 1 ||
		result.Metadata["rerank_runs"] != 1 ||
		!reflect.DeepEqual(
			result.Metadata["rerank_generation"],
			currentLongMemEvalRerankGeneration(),
		) {
		t.Fatalf("rerank provenance = %#v", result.Metadata)
	}
	if result.Metadata["reanswer_model"] != "answer-model" ||
		result.Metadata["reanswer_model_variant"] != "glm" ||
		!reflect.DeepEqual(
			result.Metadata["reanswer_build"],
			result.Metadata["rerank_build"],
		) {
		t.Fatalf("reranked answer provenance = %#v", result.Metadata)
	}
	if _, ok := result.Metadata["judge_model"]; ok {
		t.Fatalf("stale judge metadata retained: %#v", result.Metadata)
	}
	if _, err := loadLongMemEvalResults(outPath); err != nil {
		t.Fatalf("load rerank checkpoint: %v", err)
	}
}

func TestRerankLongMemEvalResultValidation(t *testing.T) {
	restoreIntFlag(t, flagLMERerankTopN, 0)
	restoreIntFlag(t, flagLMERerankRuns, 1)
	llm := &queuedAnswerModel{}
	if err := rerankLongMemEvalResult(
		context.Background(), &runResult{}, llm, "", "", "", t.TempDir()+"/out.json",
	); err == nil {
		t.Fatal("non-positive rerank topN should fail")
	}
	if err := rerankLongMemEvalResult(
		context.Background(), nil, llm, "", "", "", t.TempDir()+"/out.json",
	); err == nil {
		t.Fatal("nil result should fail")
	}
	restoreIntFlag(t, flagLMERerankTopN, 1)
	restoreIntFlag(t, flagLMERerankRuns, 0)
	if err := rerankLongMemEvalResult(
		context.Background(), &runResult{}, llm, "", "", "", t.TempDir()+"/out.json",
	); err == nil || !strings.Contains(err.Error(), "rerank-runs") {
		t.Fatalf("non-positive rerank runs error = %v", err)
	}
	restoreIntFlag(t, flagLMERerankRuns, 1)
	if err := rerankLongMemEvalResult(
		context.Background(), &runResult{Cases: []*caseResult{{
			BackendResults: map[string]*backendResult{"pgvector": {}},
		}}}, llm, "", "", "", t.TempDir()+"/out.json",
	); err == nil || !strings.Contains(err.Error(), "top_k") {
		t.Fatalf("missing source top_k error = %v", err)
	}
}

func rerankTestBackend(hits []memoryHit) *backendResult {
	return &backendResult{
		Retrieval:   hits,
		TokenUsage:  tokenUsagePtr(lmeTokenUsage{TotalTokens: 100, LLMCalls: 2}),
		AnswerUsage: tokenUsagePtr(lmeTokenUsage{TotalTokens: 20, LLMCalls: 1}),
		Judge:       &lmeJudgeResult{Correct: false},
	}
}
