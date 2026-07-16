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
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
)

func newSessionService(cfg Config) session.Service {
	return sessioninmemory.NewSessionService(
		sessioninmemory.WithSessionEventLimit(cfg.SessionEventLimit),
	)
}

const (
	seedAgentName        = "memory-eval-seed"
	defaultAgentName     = "memory-eval-agent"
	seedSessionDateLabel = "SessionDate"
)

// sessionDateLayouts lists date formats found in LoCoMo dataset.
var sessionDateLayouts = []string{
	// "8 May, 2023" / "25 May, 2023"
	"2 January, 2006",
	// "8 May 2023" (no comma)
	"2 January 2006",
	// ISO "2023-05-08"
	time.DateOnly,
	// RFC3339 "2023-05-08T13:56:00Z"
	time.RFC3339,
}

// parseSessionDate parses the SessionDate string from LoCoMo
// dataset into a time.Time. It handles formats like
// "1:56 pm on 8 May, 2023" and "8 May, 2023".
// Returns zero time and false if parsing fails.
func parseSessionDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	// Strip "<time> on " prefix if present.
	if idx := strings.Index(
		strings.ToLower(raw), " on ",
	); idx >= 0 {
		raw = strings.TrimSpace(raw[idx+len(" on "):])
	}
	for _, layout := range sessionDateLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// noAutoMemoryService wraps a memory service and disables auto extraction.
// This prevents QA interactions from contaminating the memory store.
type noAutoMemoryService struct {
	inner memory.Service
}

func (s *noAutoMemoryService) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	mem string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return s.inner.AddMemory(ctx, userKey, mem, topics, opts...)
}

func (s *noAutoMemoryService) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	mem string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return s.inner.UpdateMemory(ctx, memoryKey, mem, topics, opts...)
}

func (s *noAutoMemoryService) DeleteMemory(
	ctx context.Context,
	memoryKey memory.Key,
) error {
	return s.inner.DeleteMemory(ctx, memoryKey)
}

func (s *noAutoMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return s.inner.ClearMemories(ctx, userKey)
}

func (s *noAutoMemoryService) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return s.inner.ReadMemories(ctx, userKey, limit)
}

func (s *noAutoMemoryService) SearchMemories(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	opts ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return s.inner.SearchMemories(ctx, userKey, query, opts...)
}

func (s *noAutoMemoryService) Tools() []tool.Tool {
	return s.inner.Tools()
}

func (s *noAutoMemoryService) EnqueueAutoMemoryJob(
	_ context.Context,
	_ *session.Session,
) error {
	return nil
}

func (s *noAutoMemoryService) Close() error {
	return s.inner.Close()
}

// seedAgent is a minimal agent used to trigger Runner's auto memory enqueue.
// It does not call an LLM and produces a deterministic response.
type seedAgent struct{}

func (seedAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		if invocation == nil {
			return
		}
		rsp := &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage("OK.")},
			},
		}
		_ = event.EmitEvent(ctx, ch, event.NewResponseEvent(
			invocation.InvocationID,
			seedAgentName,
			rsp,
		))
	}()
	return ch, nil
}

func (seedAgent) Tools() []tool.Tool {
	return nil
}

func (seedAgent) Info() agent.Info {
	return agent.Info{Name: seedAgentName, Description: "Seed agent for benchmarks."}
}

func (seedAgent) SubAgents() []agent.Agent {
	return nil
}

func (seedAgent) FindSubAgent(_ string) agent.Agent {
	return nil
}

func sessionMessages(sample *dataset.LoCoMoSample, sess dataset.Session) []model.Message {
	msgs := make([]model.Message, 0, len(sess.Turns)+1)
	if strings.TrimSpace(sess.SessionDate) != "" {
		msgs = append(msgs, model.NewSystemMessage(
			fmt.Sprintf("%s: %s", seedSessionDateLabel, sess.SessionDate),
		))
	}

	primarySpeaker := ""
	secondarySpeaker := ""
	if sample != nil {
		if len(sample.Speakers) > 0 {
			primarySpeaker = sample.Speakers[0]
		}
		if len(sample.Speakers) > 1 {
			secondarySpeaker = sample.Speakers[1]
		}
	}

	for _, turn := range sess.Turns {
		role := model.RoleUser
		speakerLower := strings.ToLower(turn.Speaker)
		if secondarySpeaker != "" && turn.Speaker == secondarySpeaker {
			role = model.RoleAssistant
		} else if primarySpeaker != "" && turn.Speaker == primarySpeaker {
			role = model.RoleUser
		} else if strings.Contains(speakerLower, "assistant") {
			role = model.RoleAssistant
		} else if speakerLower == "user2" {
			role = model.RoleAssistant
		}

		content := strings.TrimSpace(turn.Text)
		if content == "" {
			continue
		}
		// Prefix with speaker name so the extractor and QA agent
		// know who said what in multi-speaker conversations.
		if turn.Speaker != "" {
			content = fmt.Sprintf("[%s]: %s", turn.Speaker, content)
		}
		msgs = append(msgs, model.Message{Role: role, Content: content})
	}
	return msgs
}

// buildHistoryMessages constructs the most recent k conversation
// turns (messages) from the sample's full conversation. It walks
// sessions in order and collects all turns, then returns the
// trailing k messages. Returns nil when k <= 0.
func buildHistoryMessages(
	sample *dataset.LoCoMoSample, k int,
) []model.Message {
	if k <= 0 || sample == nil {
		return nil
	}
	// Collect all conversation turns into messages.
	var all []model.Message
	for _, sess := range sample.Conversation {
		msgs := sessionMessages(sample, sess)
		all = append(all, msgs...)
	}
	if len(all) <= k {
		return all
	}
	return all[len(all)-k:]
}

const (
	fallbackAnswer = "The information is not available."

	// MemoryQAPromptVersion identifies the shared memory-search QA protocol.
	MemoryQAPromptVersion = "locomo-memory-qa-v2"
)

const qaInstructionHeader = `You are a memory retrieval assistant. Search memories, then output one concise answer.

`

const qaSingleSearchWorkflow = `SEARCH WORKFLOW:
1. In your first response, call memory_search exactly once and do not answer yet, even if the answer already seems obvious or unavailable.
2. Use a short keyword query containing the key entity and requested event, action, or relation.
3. Never use the kind filter. If the question is time-related, use a wide time window. For temporal ordering, set order_by_event_time=true.
4. After the search result arrives, read every returned memory and answer without calling another tool.

`

const qaMultiSearchWorkflow = `SEARCH WORKFLOW:
1. In your first response, emit exactly %[1]d separate memory_search tool calls and do not answer yet, even if the answer already seems obvious or unavailable.
2. Give each call a different short query. Include the exact key entity and vary the requested event, action, relation, or wording. Never use the kind filter.
3. If the question is time-related, use a wide time window in one query. For temporal ordering, set order_by_event_time=true. Use another query without a time filter when the filter might hide evidence.
4. After all %[1]d results arrive, read every returned memory and answer without calling another tool.

`

const qaAnswerPolicy = `EVIDENCE POLICY:
1. Topical relevance is not answer support. For a factual question, answer only when the memories support the exact subject and the requested event, action, attribute, or relation.
2. Never transfer a fact between people, objects, or similar events. Mentioning the requested person elsewhere is insufficient. A plan or consideration does not prove a choice or completed action, and possession of a related object does not prove participation in the activity.
3. Respect negative evidence and status qualifiers such as not, never, considering, planning, and completed.
4. For an explicitly hypothetical, comparative, or inferential question, make a concise inference only from evidence about the exact subject. Do not assume an unsupported premise from the question.
5. Temporal and multi-hop answers may combine multiple memories, but every required link must be supported.
6. If the exact factual relation is unsupported after all searches, output exactly "` + fallbackAnswer + `". Prefer this fallback to a guess based only on a related topic.

OUTPUT RULES:
- Output only the bare answer, with no explanation or context. Never output an empty answer.
- Prefer exact words from the supporting memories and keep the answer to 1-12 words.
- For a yes/no question, output exactly "Yes" or "No" with no supporting details.
- For "when", use a natural-language date. For "how many", output the number.
- Do not prepend a person's name or pronoun unless it is part of the requested answer.
- If uncertain whether the evidence supports the exact factual relation, output exactly "` + fallbackAnswer + `".`

func qaMemorySearchInstruction(searchPasses int) string {
	if searchPasses <= 1 {
		return qaInstructionHeader + qaSingleSearchWorkflow + qaAnswerPolicy
	}
	return qaInstructionHeader +
		fmt.Sprintf(qaMultiSearchWorkflow, searchPasses) +
		qaAnswerPolicy
}

const memoryQAMaxTokens = 512

func memoryQAGenerationConfig() model.GenerationConfig {
	reasoningEffort := "low"
	thinkingEnabled := false
	return model.GenerationConfig{
		Stream:          false,
		MaxTokens:       intPtr(memoryQAMaxTokens),
		Temperature:     float64Ptr(0),
		ReasoningEffort: &reasoningEffort,
		ThinkingEnabled: &thinkingEnabled,
	}
}

const (
	rateLimitCode              = "\"code\":\"4029\""
	maxRateLimitRetries        = 10
	rateLimitInitialBackoff    = 2 * time.Second
	rateLimitMaxBackoff        = 90 * time.Second
	rateLimitBackoffMultiplier = 2
)

// ToolCallTrace records a single tool invocation within a QA step.
type ToolCallTrace struct {
	Name   string `json:"name"`
	Args   string `json:"args,omitempty"`
	Result string `json:"result,omitempty"`
}

// StepTrace records one LLM round-trip (request → response).
type StepTrace struct {
	Step             int             `json:"step"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	CachedTokens     int             `json:"cached_tokens,omitempty"`
	FinishReason     string          `json:"finish_reason,omitempty"`
	ToolCalls        []ToolCallTrace `json:"tool_calls,omitempty"`
}

// collectResult holds the output of collecting events from a runner.
type collectResult struct {
	text  string
	usage TokenUsage
	steps []StepTrace
}

func collectFinalTextAndUsage(
	eventChan <-chan *event.Event,
) (collectResult, error) {
	var res collectResult
	step := 0
	pendingStep := -1
	for ev := range eventChan {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return res, fmt.Errorf(
				"runner event error: %s", ev.Error.Message,
			)
		}
		if ev.Response != nil {
			if len(ev.Response.Choices) > 0 {
				choice := ev.Response.Choices[0]
				msg := choice.Message
				if msg.Role == model.RoleAssistant {
					step++
					st := StepTrace{Step: step}
					if ev.Response.Usage != nil {
						st.PromptTokens =
							ev.Response.Usage.PromptTokens
						st.CompletionTokens =
							ev.Response.Usage.CompletionTokens
						st.TotalTokens =
							ev.Response.Usage.TotalTokens
						st.CachedTokens =
							ev.Response.Usage.PromptTokensDetails.CachedTokens
					}
					if choice.FinishReason != nil {
						st.FinishReason = *choice.FinishReason
					}
					for _, tc := range msg.ToolCalls {
						st.ToolCalls = append(st.ToolCalls,
							ToolCallTrace{
								Name: tc.Function.Name,
								Args: string(tc.Function.Arguments),
							})
					}
					res.steps = append(res.steps, st)
					pendingStep = -1
					if len(st.ToolCalls) > 0 {
						pendingStep = len(res.steps) - 1
					}
					if msg.Content != "" {
						res.text = msg.Content
					}
				}
				// Tool response event.
				if ev.Response.Object ==
					model.ObjectTypeToolResponse &&
					msg.Role == model.RoleTool {
					matched := false
					if pendingStep >= 0 {
						calls := res.steps[pendingStep].ToolCalls
						for i := range calls {
							if calls[i].Result != "" {
								continue
							}
							calls[i].Result = msg.Content
							matched = true
							break
						}
					}
					if !matched && len(res.steps) > 0 {
						last := &res.steps[len(res.steps)-1]
						last.ToolCalls = append(last.ToolCalls,
							ToolCallTrace{
								Name:   msg.ToolName,
								Result: msg.Content,
							})
					}
				}
			}
			if ev.Response.Usage != nil {
				res.usage.PromptTokens +=
					ev.Response.Usage.PromptTokens
				res.usage.CompletionTokens +=
					ev.Response.Usage.CompletionTokens
				res.usage.TotalTokens +=
					ev.Response.Usage.TotalTokens
				res.usage.CachedTokens +=
					ev.Response.Usage.PromptTokensDetails.CachedTokens
				res.usage.LLMCalls++
			}
		}
		if ev.IsRunnerCompletion() {
			break
		}
	}
	res.text = strings.TrimSpace(res.text)
	return res, nil
}

func collectFinalText(eventChan <-chan *event.Event) (string, error) {
	res, err := collectFinalTextAndUsage(eventChan)
	return res.text, err
}

func memorySearchProtocol(
	steps []StepTrace,
	expected int,
) (int, string) {
	if expected < 1 {
		expected = 1
	}
	var total int
	var initial int
	for stepIndex, step := range steps {
		for _, call := range step.ToolCalls {
			if call.Name != "memory_search" {
				continue
			}
			total++
			if stepIndex == 0 {
				initial++
			}
		}
	}
	if total != expected {
		return total, fmt.Sprintf(
			"memory_search calls = %d, want %d",
			total, expected,
		)
	}
	if initial != expected {
		return total, fmt.Sprintf(
			"initial memory_search calls = %d, want %d",
			initial, expected,
		)
	}
	return total, ""
}

// memorySearchResult matches the JSON structure returned by
// memory_search tool for parsing in logs.
type memorySearchResult struct {
	Query   string `json:"query"`
	Results []struct {
		ID       string `json:"id"`
		Memory   string `json:"memory"`
		Score    any    `json:"score"`
		Metadata any    `json:"metadata,omitempty"`
	} `json:"results"`
}

// logQATrace prints detailed per-step tool call traces for a QA.
func logQATrace(
	questionID, question, expected, predicted string,
	m metrics.QAMetrics,
	res collectResult,
	latencyMs int64,
) {
	_ = questionID
	log.Printf("    📋 Question: %s", question)
	log.Printf("    🎯 Expected: %s", expected)
	for _, st := range res.steps {
		if st.CachedTokens > 0 {
			log.Printf(
				"    🔹 Step %d | Tokens: %d"+
					" (in:%d cached:%d out:%d)",
				st.Step, st.TotalTokens,
				st.PromptTokens, st.CachedTokens,
				st.CompletionTokens,
			)
		} else {
			log.Printf(
				"    🔹 Step %d | Tokens: %d"+
					" (in:%d out:%d)",
				st.Step, st.TotalTokens,
				st.PromptTokens, st.CompletionTokens,
			)
		}
		if st.FinishReason != "" {
			log.Printf(
				"    Finish reason: %s", st.FinishReason,
			)
		}
		if len(st.ToolCalls) > 0 {
			log.Printf(
				"    🔧 Tool Calls: %d", len(st.ToolCalls),
			)
			for i, tc := range st.ToolCalls {
				log.Printf(
					"      [%d] %s", i+1, tc.Name,
				)
				if tc.Args != "" {
					log.Printf(
						"          Args: %s", tc.Args,
					)
				}
				if tc.Result != "" {
					log.Printf(
						"      ✅ Tool Result [%s]:",
						tc.Name,
					)
					// Special formatting for memory_search.
					if tc.Name == "memory_search" {
						formatMemorySearchResult(tc.Result)
					} else {
						log.Printf("          %s", tc.Result)
					}
				}
			}
		}
	}
	log.Printf(
		"    💬 Predicted: %s", predicted,
	)
	log.Printf(
		"    📊 F1=%.3f BLEU=%.3f LLM=%.3f | %dms",
		m.F1, m.BLEU, m.LLMScore, latencyMs,
	)
}

func logDirectQATrace(
	question, expected, predicted string,
	m metrics.QAMetrics,
	latencyMs int64,
	usage *TokenUsage,
	tokensUsed int,
) {
	log.Printf("    📋 Question: %s", question)
	log.Printf("    🎯 Expected: %s", expected)
	if usage != nil {
		if usage.CachedTokens > 0 {
			log.Printf(
				"    🔹 Tokens: %d (in:%d cached:%d out:%d)",
				usage.TotalTokens,
				usage.PromptTokens,
				usage.CachedTokens,
				usage.CompletionTokens,
			)
		} else {
			log.Printf(
				"    🔹 Tokens: %d (in:%d out:%d)",
				usage.TotalTokens,
				usage.PromptTokens,
				usage.CompletionTokens,
			)
		}
	} else if tokensUsed > 0 {
		log.Printf("    🔹 Tokens (estimated): %d", tokensUsed)
	}
	log.Printf("    💬 Predicted: %s", predicted)
	log.Printf(
		"    📊 F1=%.3f BLEU=%.3f LLM=%.3f | %dms",
		m.F1, m.BLEU, m.LLMScore, latencyMs,
	)
}

func logSessionRecallTrace(trace *SessionRecallTrace) {
	if trace == nil {
		return
	}
	log.Printf(
		"    🧠 Session Recall: mode=%s k=%d min_score=%.2f hits=%d",
		trace.SearchMode,
		trace.MaxResults,
		trace.MinScore,
		len(trace.Hits),
	)
	for i, hit := range trace.Hits {
		log.Printf(
			"      [%d] session=%s role=%s score=%.4f dense=%.4f sparse=%.4f event=%s",
			i+1,
			hit.SessionID,
			hit.Role,
			hit.Score,
			hit.DenseScore,
			hit.SparseScore,
			hit.EventID,
		)
		log.Printf(
			"          %s",
			truncateLogLine(hit.Text, 240),
		)
	}
}

func truncateLogLine(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}

func logVerboseQAResult(
	index, total int,
	qa dataset.QAItem,
	qaResult *QAResult,
) {
	log.Printf("  [QA %d/%d] %s (%s)",
		index+1, total,
		qa.QuestionID, qa.Category,
	)
	if qaResult == nil {
		return
	}
	if qaResult.ProtocolError != "" {
		log.Printf(
			"    Protocol violation: %s", qaResult.ProtocolError,
		)
	}
	if qaResult.SessionRecall != nil {
		logSessionRecallTrace(qaResult.SessionRecall)
	}
	if len(qaResult.Steps) > 0 {
		logQATrace(
			qa.QuestionID,
			qa.Question,
			qa.Answer,
			qaResult.Predicted,
			qaResult.Metrics,
			collectResult{steps: qaResult.Steps},
			qaResult.LatencyMs,
		)
		return
	}
	logDirectQATrace(
		qa.Question,
		qa.Answer,
		qaResult.Predicted,
		qaResult.Metrics,
		qaResult.LatencyMs,
		qaResult.TokenUsage,
		qaResult.TokensUsed,
	)
}

// formatMemorySearchResult parses and pretty-prints memory_search
// results, showing each recalled memory on its own line.
func formatMemorySearchResult(result string) {
	var msr memorySearchResult
	if err := json.Unmarshal([]byte(result), &msr); err != nil {
		log.Printf("          %s", result)
		return
	}
	if len(msr.Results) == 0 {
		log.Printf("          (no results)")
		return
	}
	for j, r := range msr.Results {
		log.Printf("          [%d] %s", j+1, r.Memory)
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "429 Too Many Requests") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "Rate limit") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "Too Many Requests") ||
		strings.Contains(msg, "rate_limit_exceeded") ||
		strings.Contains(msg, "server_busy") ||
		strings.Contains(msg, rateLimitCode)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runWithRateLimitRetry(
	ctx context.Context,
	run func() (<-chan *event.Event, error),
) (collectResult, error) {
	backoff := rateLimitInitialBackoff
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		ch, err := run()
		if err != nil {
			if isRateLimitError(err) {
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return collectResult{}, sleepErr
				}
				backoff = minDuration(backoff*time.Duration(rateLimitBackoffMultiplier), rateLimitMaxBackoff)
				continue
			}
			return collectResult{}, err
		}

		res, err := collectFinalTextAndUsage(ch)
		if err != nil {
			if isRateLimitError(err) {
				if sleepErr := sleepWithContext(ctx, backoff); sleepErr != nil {
					return collectResult{}, sleepErr
				}
				backoff = minDuration(backoff*time.Duration(rateLimitBackoffMultiplier), rateLimitMaxBackoff)
				continue
			}
			return collectResult{}, err
		}
		return res, nil
	}
	return collectResult{}, fmt.Errorf("rate limit retry exceeded")
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
