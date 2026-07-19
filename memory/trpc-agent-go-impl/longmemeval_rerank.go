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
	"errors"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	lmeRerankPromptVersion  = "lme-memory-rerank-v3"
	lmeRerankInitialTokens  = 512
	lmeRerankRetryTokens    = 4096
	lmeRerankMaxAttempts    = 2
	lmeRerankRRFK           = 60
	lmeRerankRetryDirective = `Your previous attempt was truncated before the required JSON.
Return the JSON object before any analysis. The first character must be "{".
Do not repeat your reasoning.`
)

func rerankLongMemEvalHits(
	ctx context.Context,
	llm model.Model,
	inst *lmeInstance,
	hits []memoryHit,
	topN int,
	runs int,
) ([]memoryHit, string, error) {
	if llm == nil {
		return nil, "", errors.New("rerank model is nil")
	}
	if inst == nil {
		return nil, "", errors.New("rerank instance is nil")
	}
	if len(hits) == 0 {
		return []memoryHit{}, "", nil
	}
	if topN <= 0 {
		return nil, "", fmt.Errorf("rerank topN must be positive, got %d", topN)
	}
	if runs <= 0 {
		return nil, "", fmt.Errorf("rerank runs must be positive, got %d", runs)
	}
	if topN > len(hits) {
		topN = len(hits)
	}
	prompt := buildLongMemEvalRerankPrompt(inst, hits, topN)
	rankings := make([][]int, 0, runs)
	trace := lmeRerankConsensusTrace{Runs: make([]lmeRerankConsensusRun, 0, runs)}
	for run := 0; run < runs; run++ {
		indices, raw, err := runLongMemEvalRerank(ctx, llm, prompt, len(hits), topN)
		entry := lmeRerankConsensusRun{Raw: raw, Indices: indices}
		if err != nil {
			entry.Error = err.Error()
			trace.Runs = append(trace.Runs, entry)
			return nil, marshalLongMemEvalRerankTrace(trace), fmt.Errorf(
				"rerank run %d/%d: %w", run+1, runs, err,
			)
		}
		trace.Runs = append(trace.Runs, entry)
		rankings = append(rankings, indices)
	}
	if runs == 1 {
		return longMemEvalHitsByIndices(hits, rankings[0]), trace.Runs[0].Raw, nil
	}
	trace.FusedIndices = fuseLongMemEvalRerankIndices(rankings, topN)
	return longMemEvalHitsByIndices(hits, trace.FusedIndices),
		marshalLongMemEvalRerankTrace(trace), nil
}

type lmeRerankConsensusRun struct {
	Raw     string `json:"raw"`
	Indices []int  `json:"indices,omitempty"`
	Error   string `json:"error,omitempty"`
}

type lmeRerankConsensusTrace struct {
	Runs         []lmeRerankConsensusRun `json:"runs"`
	FusedIndices []int                   `json:"fused_indices,omitempty"`
}

func runLongMemEvalRerank(
	ctx context.Context,
	llm model.Model,
	prompt string,
	candidateCount int,
	topN int,
) ([]int, string, error) {
	var lastRaw string
	for attempt := 0; attempt < lmeRerankMaxAttempts; attempt++ {
		maxTokens := lmeRerankInitialTokens
		requestPrompt := prompt
		if attempt > 0 {
			maxTokens = lmeRerankRetryTokens
			requestPrompt = lmeRerankRetryDirective + "\n\n" + prompt
		}
		raw, finishReason, err := generateLongMemEvalRerankResponse(
			ctx,
			llm,
			newLongMemEvalRerankRequest(requestPrompt, maxTokens),
		)
		if err != nil {
			return nil, raw, err
		}
		lastRaw = raw
		indices, parseErr := parseLongMemEvalRerankIndices(raw, candidateCount, topN)
		if parseErr == nil {
			return indices, raw, nil
		}
		if attempt+1 == lmeRerankMaxAttempts ||
			!isLongMemEvalRerankLengthFinish(finishReason) {
			return nil, raw, parseErr
		}
	}
	return nil, lastRaw, errors.New("rerank exhausted attempts")
}

func fuseLongMemEvalRerankIndices(rankings [][]int, topN int) []int {
	scores := make(map[int]float64)
	bestRanks := make(map[int]int)
	for _, ranking := range rankings {
		for rank, index := range ranking {
			scores[index] += 1 / float64(lmeRerankRRFK+rank+1)
			if best, ok := bestRanks[index]; !ok || rank < best {
				bestRanks[index] = rank
			}
		}
	}
	indices := make([]int, 0, len(scores))
	for index := range scores {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool {
		left, right := indices[i], indices[j]
		if scores[left] != scores[right] {
			return scores[left] > scores[right]
		}
		if bestRanks[left] != bestRanks[right] {
			return bestRanks[left] < bestRanks[right]
		}
		return left < right
	})
	if topN > 0 && len(indices) > topN {
		indices = indices[:topN]
	}
	return indices
}

func longMemEvalHitsByIndices(hits []memoryHit, indices []int) []memoryHit {
	reranked := make([]memoryHit, 0, len(indices))
	for _, index := range indices {
		reranked = append(reranked, hits[index-1])
	}
	return reranked
}

func marshalLongMemEvalRerankTrace(trace lmeRerankConsensusTrace) string {
	data, err := json.Marshal(trace)
	if err != nil {
		return ""
	}
	return string(data)
}

func generateLongMemEvalRerankResponse(
	ctx context.Context,
	llm model.Model,
	req *model.Request,
) (string, string, error) {
	responses, err := llm.GenerateContent(ctx, req)
	if err != nil {
		return "", "", err
	}
	if responses == nil {
		return "", "", errors.New("rerank model returned nil response channel")
	}
	var content, delta strings.Builder
	var finishReason string
	for response := range responses {
		if response == nil {
			continue
		}
		if response.Error != nil {
			return longMemEvalRerankContent(&content, &delta), finishReason,
				errors.New(response.Error.Message)
		}
		if len(response.Choices) == 0 {
			continue
		}
		choice := response.Choices[0]
		if choice.Delta.Content != "" {
			delta.WriteString(choice.Delta.Content)
		}
		if choice.Message.Content != "" {
			content.Reset()
			content.WriteString(choice.Message.Content)
		}
		if choice.FinishReason != nil {
			finishReason = *choice.FinishReason
		}
	}
	return longMemEvalRerankContent(&content, &delta), finishReason, nil
}

func longMemEvalRerankContent(content, delta *strings.Builder) string {
	raw := strings.TrimSpace(content.String())
	if raw == "" {
		raw = strings.TrimSpace(delta.String())
	}
	return raw
}

func isLongMemEvalRerankLengthFinish(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}

func newLongMemEvalRerankRequest(prompt string, maxTokens int) *model.Request {
	temperature := 0.0
	reasoningEffort := "low"
	thinkingEnabled := false
	return &model.Request{
		Messages: []model.Message{model.NewUserMessage(prompt)},
		GenerationConfig: model.GenerationConfig{
			Stream:          false,
			MaxTokens:       &maxTokens,
			Temperature:     &temperature,
			ReasoningEffort: &reasoningEffort,
			ThinkingEnabled: &thinkingEnabled,
		},
		ExtraFields: map[string]any{
			"response_format": map[string]string{"type": "json_object"},
		},
	}
}

func buildLongMemEvalRerankPrompt(
	inst *lmeInstance,
	hits []memoryHit,
	topN int,
) string {
	var candidates strings.Builder
	for i, hit := range hits {
		fmt.Fprintf(&candidates, "%d. %s", i+1, hit.Memory)
		if meta := formatMemoryMetadata(
			hit.Kind, hit.EventTime, hit.Topics,
			hit.Participants, hit.Location,
		); meta != "" {
			fmt.Fprintf(&candidates, " [%s]", meta)
		}
		candidates.WriteByte('\n')
	}
	return fmt.Sprintf(`You rerank long-term memories for a question.

Select only memories that directly help answer the requested subject,
predicate, constraints, or required reasoning. Shared keywords alone are not
enough. Match the exact kind of value requested: languages, resources, courses,
events, actions, preferences, and general advice are distinct even when they
share a topic. Respect grammatical roles: a user's actions are different from
lists the assistant recommended, while a question about what the assistant
said should prefer the candidate that states the exact requested result and
reject adjacent recommendations of a different kind. For preference or advice
questions, keep both the relevant personal fact or prior choice and any
directly applicable recommendation. Topical personal history can personalize
an open-ended question even when the question does not restate that history.
Do not keep generic advice without the personal evidence that makes it
relevant. For order, date, count, comparison,
or multi-hop questions, select every distinct fact needed to compute the answer
and prioritize explicit events, dates, quantities, and relationships. Do not
answer the question and do not use outside knowledge.

Return JSON only in this form:
{"indices":[2,5,1]}

The indices must be unique, ordered from most to least relevant, and contain at
most %d items. Return {"indices":[]} when no candidate directly supports the
answer.

Question date: %s
Question: %s

Candidates:
%s`, topN, inst.QuestionDate, inst.Question, candidates.String())
}

func parseLongMemEvalRerankIndices(
	raw string,
	candidateCount int,
	topN int,
) ([]int, error) {
	candidate := strings.TrimSpace(raw)
	start := strings.Index(candidate, "{")
	end := strings.LastIndex(candidate, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("rerank response is not a JSON object: %q", truncate(raw, 80))
	}
	candidate = candidate[start : end+1]
	var response struct {
		Indices *[]int `json:"indices"`
	}
	if err := json.Unmarshal([]byte(candidate), &response); err != nil {
		return nil, fmt.Errorf("decode rerank response %q: %w", truncate(raw, 80), err)
	}
	if response.Indices == nil {
		return nil, errors.New("rerank response omitted indices")
	}
	seen := make(map[int]bool, len(*response.Indices))
	indices := make([]int, 0, min(topN, len(*response.Indices)))
	for _, index := range *response.Indices {
		if index < 1 || index > candidateCount || seen[index] {
			continue
		}
		seen[index] = true
		indices = append(indices, index)
		if len(indices) == topN {
			break
		}
	}
	if len(*response.Indices) > 0 && len(indices) == 0 {
		return nil, errors.New("rerank response contained no valid indices")
	}
	return indices, nil
}

func replaceLongMemEvalRerankUsage(br *backendResult, usage lmeTokenUsage) {
	if br == nil {
		return
	}
	total := lmeTokenUsage{}
	if br.TokenUsage != nil {
		total = *br.TokenUsage
	}
	if br.RerankUsage != nil {
		total.Sub(*br.RerankUsage)
	}
	total.Add(usage)
	br.TokenUsage = tokenUsagePtr(total)
	br.RerankUsage = tokenUsagePtr(usage)
}
