//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTokenTrackerRecordsExtractionCall(t *testing.T) {
	tracker := NewTokenTracker()
	finishReason := "tool_calls"
	callback := tracker.AfterModelCallback()
	_, err := callback(context.Background(), &model.AfterModelArgs{
		Request: &model.Request{Messages: []model.Message{
			model.NewSystemMessage("extract memories"),
			model.NewUserMessage("I have known them for four years."),
		}},
		Response: &model.Response{
			Usage: &model.Usage{
				PromptTokens:     100,
				CompletionTokens: 20,
				TotalTokens:      120,
				PromptTokensDetails: model.PromptTokensDetails{
					CachedTokens:        11,
					CacheCreationTokens: 7,
					CacheReadTokens:     13,
				},
				CompletionTokensDetails: model.CompletionTokensDetails{
					ReasoningTokens: 5,
				},
			},
			Choices: []model.Choice{{
				FinishReason: &finishReason,
				Message: model.Message{ToolCalls: []model.ToolCall{{
					Function: model.FunctionDefinitionParam{
						Name:      "memory_add",
						Arguments: []byte(`{"memory":"Known friends for four years"}`),
					},
				}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("callback: %v", err)
	}

	usage, calls := tracker.SnapshotWithCalls()
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 ||
		usage.TotalTokens != 120 || usage.CachedTokens != 11 ||
		usage.CacheCreationTokens != 7 || usage.CacheReadTokens != 13 ||
		usage.ReasoningTokens != 5 || usage.LLMCalls != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.CachedPromptTokens() != 13 {
		t.Fatalf("cached prompt tokens = %d, want 13", usage.CachedPromptTokens())
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Step != 1 || len(call.SourceMessages) != 1 ||
		call.SourceMessages[0].Content != "I have known them for four years." ||
		len(call.ToolCalls) != 1 || call.ToolCalls[0].Name != "memory_add" ||
		call.FinishReason != finishReason {
		t.Fatalf("call = %+v", call)
	}
	if nextUsage, nextCalls := tracker.SnapshotWithCalls(); !nextUsage.IsZero() || len(nextCalls) != 0 {
		t.Fatalf("tracker did not reset: usage=%+v calls=%+v", nextUsage, nextCalls)
	}
}

func TestTokenTrackerRecordsModelErrorWithoutUsage(t *testing.T) {
	tracker := NewTokenTracker()
	callback := tracker.AfterModelCallback()
	_, err := callback(context.Background(), &model.AfterModelArgs{
		Request: &model.Request{Messages: []model.Message{
			model.NewUserMessage("remember this"),
		}},
		Error: errors.New("rate limited"),
	})
	if err != nil {
		t.Fatalf("callback: %v", err)
	}

	usage, calls := tracker.SnapshotWithCalls()
	if usage.LLMCalls != 1 || len(calls) != 1 ||
		calls[0].Error != "rate limited" {
		t.Fatalf("usage=%+v calls=%+v", usage, calls)
	}
}
