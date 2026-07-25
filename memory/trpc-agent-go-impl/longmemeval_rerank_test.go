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
