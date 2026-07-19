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
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestRerankLongMemEvalHits(t *testing.T) {
	stop := "stop"
	llm := &queuedAnswerModel{responses: []*model.Response{{
		Choices: []model.Choice{{
			Message:      model.NewAssistantMessage(`{"indices":[2]}`),
			FinishReason: &stop,
		}},
	}}}
	hits := []memoryHit{
		{ID: "1", Memory: "Assistant recommended SQL.", SourceSessions: []string{"hidden-label"}},
		{ID: "2", Memory: "Assistant recommended Ruby, Python, or PHP."},
	}
	reranked, raw, err := rerankLongMemEvalHits(
		context.Background(),
		llm,
		&lmeInstance{
			Question:     "Which languages did you recommend I learn?",
			QuestionDate: "2024-01-01",
			Answer:       flexString("gold-secret"),
		},
		hits,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("rerank hits: %v", err)
	}
	if raw != `{"indices":[2]}` || len(reranked) != 1 || reranked[0].ID != "2" {
		t.Fatalf("reranked = %#v, raw = %q", reranked, raw)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("rerank requests = %d", len(llm.requests))
	}
	responseFormat, ok := llm.requests[0].ExtraFields["response_format"].(map[string]string)
	if !ok || responseFormat["type"] != "json_object" {
		t.Fatalf("rerank response format = %#v", llm.requests[0].ExtraFields)
	}
	prompt := llm.requests[0].Messages[0].Content
	if strings.Contains(prompt, "gold-secret") || strings.Contains(prompt, "hidden-label") {
		t.Fatalf("rerank prompt leaked evaluation labels: %s", prompt)
	}
}

func TestRerankLongMemEvalHitsConsensus(t *testing.T) {
	stop := "stop"
	response := func(content string) *model.Response {
		return &model.Response{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage(content),
			FinishReason: &stop,
		}}}
	}
	llm := &queuedAnswerModel{responses: []*model.Response{
		response(`{"indices":[2,1]}`),
		response(`{"indices":[2,1]}`),
		response(`{"indices":[1,2]}`),
	}}
	hits := []memoryHit{
		{ID: "1", Memory: "Related but indirect."},
		{ID: "2", Memory: "Directly answers the question."},
	}
	reranked, raw, err := rerankLongMemEvalHits(
		context.Background(), llm,
		&lmeInstance{Question: "What directly answers the question?"},
		hits, 2, 3,
	)
	if err != nil {
		t.Fatalf("rerank consensus: %v", err)
	}
	if len(reranked) != 2 || reranked[0].ID != "2" || reranked[1].ID != "1" {
		t.Fatalf("reranked consensus = %#v", reranked)
	}
	var trace lmeRerankConsensusTrace
	if err := json.Unmarshal([]byte(raw), &trace); err != nil {
		t.Fatalf("decode consensus trace: %v", err)
	}
	if len(trace.Runs) != 3 || len(trace.FusedIndices) != 2 ||
		trace.FusedIndices[0] != 2 || trace.FusedIndices[1] != 1 {
		t.Fatalf("consensus trace = %#v", trace)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("rerank requests = %d, want 3", len(llm.requests))
	}
}

func TestRerankLongMemEvalHitsRetriesTruncatedResponse(t *testing.T) {
	length := "length"
	stop := "stop"
	llm := &queuedAnswerModel{responses: []*model.Response{
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage("analysis without JSON"),
				FinishReason: &length,
			}},
		},
		{
			Choices: []model.Choice{{
				Message:      model.NewAssistantMessage(`{"indices":[2]}`),
				FinishReason: &stop,
			}},
		},
	}}
	hits := []memoryHit{{ID: "1", Memory: "irrelevant"}, {ID: "2", Memory: "relevant"}}
	reranked, raw, err := rerankLongMemEvalHits(
		context.Background(),
		llm,
		&lmeInstance{Question: "What is relevant?"},
		hits,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("rerank hits after retry: %v", err)
	}
	if raw != `{"indices":[2]}` || len(reranked) != 1 || reranked[0].ID != "2" {
		t.Fatalf("reranked = %#v, raw = %q", reranked, raw)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("rerank requests = %d", len(llm.requests))
	}
	if llm.requests[0].MaxTokens == nil || *llm.requests[0].MaxTokens != lmeRerankInitialTokens {
		t.Fatalf("initial max tokens = %#v", llm.requests[0].MaxTokens)
	}
	if llm.requests[1].MaxTokens == nil || *llm.requests[1].MaxTokens != lmeRerankRetryTokens {
		t.Fatalf("retry max tokens = %#v", llm.requests[1].MaxTokens)
	}
	if !strings.HasPrefix(llm.requests[1].Messages[0].Content, lmeRerankRetryDirective) {
		t.Fatalf("retry prompt = %q", llm.requests[1].Messages[0].Content)
	}
}

func TestRerankLongMemEvalHitsValidation(t *testing.T) {
	ctx := context.Background()
	llm := &queuedAnswerModel{}
	inst := &lmeInstance{Question: "question"}
	hits := []memoryHit{{ID: "1", Memory: "memory"}}

	if _, _, err := rerankLongMemEvalHits(ctx, nil, inst, hits, 1, 1); err == nil ||
		!strings.Contains(err.Error(), "model is nil") {
		t.Fatalf("nil model error = %v", err)
	}
	if _, _, err := rerankLongMemEvalHits(ctx, llm, nil, hits, 1, 1); err == nil ||
		!strings.Contains(err.Error(), "instance is nil") {
		t.Fatalf("nil instance error = %v", err)
	}
	empty, raw, err := rerankLongMemEvalHits(ctx, llm, inst, nil, 1, 1)
	if err != nil || len(empty) != 0 || raw != "" {
		t.Fatalf("empty hits = %#v, raw = %q, err = %v", empty, raw, err)
	}
	if _, _, err := rerankLongMemEvalHits(ctx, llm, inst, hits, 0, 1); err == nil ||
		!strings.Contains(err.Error(), "topN must be positive") {
		t.Fatalf("invalid topN error = %v", err)
	}
	if _, _, err := rerankLongMemEvalHits(ctx, llm, inst, hits, 1, 0); err == nil ||
		!strings.Contains(err.Error(), "runs must be positive") {
		t.Fatalf("invalid runs error = %v", err)
	}
}

func TestRerankLongMemEvalHitsConsensusFailureTrace(t *testing.T) {
	stop := "stop"
	response := func(content string) *model.Response {
		return &model.Response{Choices: []model.Choice{{
			Message:      model.NewAssistantMessage(content),
			FinishReason: &stop,
		}}}
	}
	llm := &queuedAnswerModel{responses: []*model.Response{
		response(`{"indices":[1]}`),
		response("invalid"),
	}}
	_, raw, err := rerankLongMemEvalHits(
		context.Background(), llm,
		&lmeInstance{Question: "question"},
		[]memoryHit{{ID: "1", Memory: "memory"}},
		1, 3,
	)
	if err == nil || !strings.Contains(err.Error(), "run 2/3") {
		t.Fatalf("consensus error = %v", err)
	}
	var trace lmeRerankConsensusTrace
	if err := json.Unmarshal([]byte(raw), &trace); err != nil {
		t.Fatalf("decode failure trace: %v", err)
	}
	if len(trace.Runs) != 2 || trace.Runs[1].Error == "" {
		t.Fatalf("failure trace = %#v", trace)
	}
}

func TestFuseLongMemEvalRerankIndicesDeterministicTieAndLimit(t *testing.T) {
	indices := fuseLongMemEvalRerankIndices([][]int{
		{2, 1, 3},
		{1, 2, 3},
	}, 2)
	if len(indices) != 2 || indices[0] != 1 || indices[1] != 2 {
		t.Fatalf("fused indices = %#v", indices)
	}
}

func TestParseLongMemEvalRerankIndices(t *testing.T) {
	indices, err := parseLongMemEvalRerankIndices(
		"```json\n{\"indices\":[2,2,7,1]}\n```",
		3,
		2,
	)
	if err != nil {
		t.Fatalf("parse rerank indices: %v", err)
	}
	if len(indices) != 2 || indices[0] != 2 || indices[1] != 1 {
		t.Fatalf("indices = %#v", indices)
	}

	indices, err = parseLongMemEvalRerankIndices(`{"indices":[]}`, 3, 2)
	if err != nil || len(indices) != 0 {
		t.Fatalf("empty indices = %#v, err = %v", indices, err)
	}
	for _, raw := range []string{
		"not json",
		`{"other":[]}`,
		`{"indices":[0,4]}`,
	} {
		if _, err := parseLongMemEvalRerankIndices(raw, 3, 2); err == nil {
			t.Errorf("parse accepted invalid response %q", raw)
		}
	}
}

func TestReplaceLongMemEvalRerankUsage(t *testing.T) {
	br := &backendResult{
		TokenUsage:  tokenUsagePtr(lmeTokenUsage{TotalTokens: 120, LLMCalls: 3}),
		RerankUsage: tokenUsagePtr(lmeTokenUsage{TotalTokens: 20, LLMCalls: 1}),
	}
	replaceLongMemEvalRerankUsage(
		br,
		lmeTokenUsage{TotalTokens: 30, LLMCalls: 1},
	)
	if br.TokenUsage == nil || br.TokenUsage.TotalTokens != 130 ||
		br.TokenUsage.LLMCalls != 3 {
		t.Fatalf("token usage = %#v", br.TokenUsage)
	}
	if br.RerankUsage == nil || br.RerankUsage.TotalTokens != 30 {
		t.Fatalf("rerank usage = %#v", br.RerankUsage)
	}
}
