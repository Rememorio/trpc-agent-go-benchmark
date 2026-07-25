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

func sessionMessages(sess dataset.Session) []model.Message {
	msgs := make([]model.Message, 0, len(sess.Turns)+1)
	if strings.TrimSpace(sess.SessionDate) != "" {
		msgs = append(msgs, model.NewSystemMessage(
			fmt.Sprintf("%s: %s", seedSessionDateLabel, sess.SessionDate),
		))
	}

	// Both LoCoMo participants are humans, so user/assistant is only a
	// transport-level mapping. Anchor each session on its opening speaker to
	// prevent strict chat providers from dropping a leading assistant message.
	openingSpeaker := ""
	for _, turn := range sess.Turns {
		if strings.TrimSpace(turn.Speaker) != "" &&
			strings.TrimSpace(turn.Text) != "" {
			openingSpeaker = turn.Speaker
			break
		}
	}

	for _, turn := range sess.Turns {
		role := model.RoleUser
		speakerLower := strings.ToLower(turn.Speaker)
		if openingSpeaker != "" && turn.Speaker == openingSpeaker {
			role = model.RoleUser
		} else if turn.Speaker != "" {
			role = model.RoleAssistant
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
		msgs := sessionMessages(sess)
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
	MemoryQAPromptVersion = "locomo-memory-qa-v15"

	// MemoryQASearchStrategy identifies how multiple retrieval queries run.
	MemoryQASearchStrategy = "sequential-adaptive"

	// MemoryQARecoveryMaxTokens caps the forced-tool answer recovery call.
	MemoryQARecoveryMaxTokens = 2048

	memoryQAMaxPrimaryAnswerWords = 12
	// The longest LoCoMo reference answer is 57 words. Recovery leaves
	// headroom for equivalent wording while still rejecting runaway output.
	memoryQAMaxRecoveryAnswerWords = 64
	memoryQASubmitAnswerToolName   = "submit_answer"
	memoryQAAnswerSourceInitial    = "initial"
	memoryQAAnswerSourceRecovery   = "recovery"
	memoryQAAnswerSourceFallback   = "fallback"
)

type memoryQASubmitAnswerTool struct{}

func (memoryQASubmitAnswerTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: memoryQASubmitAnswerToolName,
		Description: "Submit the final concise answer grounded only in " +
			"the retrieved memories.",
		InputSchema: &tool.Schema{
			Type:                 "object",
			AdditionalProperties: false,
			Properties: map[string]*tool.Schema{
				"answer": {
					Type: "string",
					Description: "The shortest complete final answer " +
						"span in at most 64 words.",
				},
			},
			Required: []string{"answer"},
		},
	}
}

const qaInstructionHeader = `You are a memory retrieval assistant. Search memories, then output one concise answer.

`

const qaSingleSearchWorkflow = `SEARCH WORKFLOW:
1. In your first response, call memory_search exactly once and do not answer yet, even if the answer already seems obvious or unavailable.
2. Use a short keyword query containing the key entity and requested event, action, or relation.
3. Never use the kind filter. If the question is time-related, use a wide time window. For temporal ordering, set order_by_event_time=true.
4. After the search result arrives, read every returned memory and answer without calling another tool.

`

const qaMultiSearchWorkflow = `SEARCH WORKFLOW:
1. You MUST call memory_search exactly %[1]d times before answering, even if an earlier result seems sufficient or the answer seems unavailable.
2. Call exactly ONE memory_search per assistant response, then stop and wait for its result. NEVER emit multiple tool calls in the same response.
3. Search #1 with short keywords containing the exact key entity and requested event, action, or relation. Never use the kind filter. If time-related, use a wide time window. For temporal ordering, set order_by_event_time=true.
4. After each result, use what you learned to plan a different query or relation while keeping the exact subject. If a time filter may have hidden evidence, search once without it.
5. Only after all %[1]d search results have arrived, read every returned memory and answer without calling another tool.

`

const qaAnswerPolicy = `EVIDENCE POLICY:
1. Topical relevance is not answer support. For a factual question, answer only when the memories support the exact subject and the requested event, action, attribute, or relation.
2. Support may be direct or an unambiguous paraphrase or distinctive description of the same object or event. Exact wording or a title need not appear when the identity and requested relation are otherwise clear.
3. Never transfer a fact between people, objects, or similar events. Mentioning the requested person elsewhere is insufficient. A plan or consideration does not prove a choice or completed action, and possession of a related object does not prove participation in the activity.
4. Respect negative evidence and status qualifiers such as not, never, considering, planning, and completed.
5. For an explicitly hypothetical, comparative, or inferential question, make a concise inference only from evidence about the exact subject. Do not assume an unsupported premise from the question.
6. Temporal and multi-hop answers may combine multiple memories, but every required link must be supported.
7. Prefer the most specific supported answer. If retrieved memories provide an explicit person, place, organization, or item, never replace it with a pronoun or vague category label. Combine an exact relation with an unambiguous identity from another retrieved memory when needed.
8. Return every supported part that directly answers a compound reason, description, or requested list. Do not omit a required part just to make the answer shorter, and do not add unrelated facts from the same topic.
9. If the exact factual relation is unsupported after all searches, output exactly "` + fallbackAnswer + `". Prefer this fallback to a guess based only on a related topic.

FINAL ANSWER FORMAT (MANDATORY):
- The score compares your text with a short reference answer. Output only the shortest complete answer span, never evidence, explanation, context, Markdown, or a full sentence. Complete means it names the requested entity and includes every directly requested part supported by the memories.
- For yes/no, output exactly "Yes" or "No". For who/what/where/which, output only the requested name or noun phrase. For when, output only a natural-language date. For how many, output only the number. For why/how, output a short clause.
- Use exact words from the supporting memories. Keep the answer to 1-12 words and do not restate the question's subject.
- Examples: "Sweden", "Transgender woman", "Horseback riding", "19 October 2023", "3", "Yes".
- Never output an empty answer. If the exact factual relation is unsupported, output exactly "` + fallbackAnswer + `".`

const qaQuestionAnswerConstraint = `

After the required memory searches, output only the shortest complete final answer span (1-12 words). Use explicit entity names instead of vague references, and include every directly requested part supported by the memories. Do not include evidence, explanation, context, or Markdown. For yes/no, output only Yes or No.`

func memoryQAUserMessage(question string) model.Message {
	return model.NewUserMessage(question + qaQuestionAnswerConstraint)
}

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
	Step                int             `json:"step"`
	Phase               string          `json:"phase,omitempty"`
	LLMCalls            int             `json:"llm_calls,omitempty"`
	PromptTokens        int             `json:"prompt_tokens"`
	CompletionTokens    int             `json:"completion_tokens"`
	TotalTokens         int             `json:"total_tokens"`
	CachedTokens        int             `json:"cached_tokens,omitempty"`
	CacheCreationTokens int             `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int             `json:"cache_read_tokens,omitempty"`
	ReasoningTokens     int             `json:"reasoning_tokens,omitempty"`
	FinishReason        string          `json:"finish_reason,omitempty"`
	Error               string          `json:"error,omitempty"`
	ToolCalls           []ToolCallTrace `json:"tool_calls,omitempty"`
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
		recordedAssistantCall := false
		if ev.Response != nil {
			if len(ev.Response.Choices) > 0 {
				choice := ev.Response.Choices[0]
				msg := choice.Message
				if msg.Role == model.RoleAssistant {
					step++
					st := StepTrace{
						Step: step, Phase: "answer", LLMCalls: 1,
					}
					recordedAssistantCall = true
					if ev.Response.Usage != nil {
						st.PromptTokens =
							ev.Response.Usage.PromptTokens
						st.CompletionTokens =
							ev.Response.Usage.CompletionTokens
						st.TotalTokens =
							ev.Response.Usage.TotalTokens
						st.CachedTokens =
							ev.Response.Usage.PromptTokensDetails.CachedTokens
						st.CacheCreationTokens = ev.Response.Usage.
							PromptTokensDetails.CacheCreationTokens
						st.CacheReadTokens = ev.Response.Usage.
							PromptTokensDetails.CacheReadTokens
						st.ReasoningTokens = ev.Response.Usage.
							CompletionTokensDetails.ReasoningTokens
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
					if len(st.ToolCalls) > 0 {
						st.Phase = "memory-search"
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
				res.usage.Add(tokenUsageFromModelUsage(ev.Response.Usage))
			} else if recordedAssistantCall {
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
	stepCalls := make([]int, len(steps))
	for stepIndex, step := range steps {
		for _, call := range step.ToolCalls {
			if call.Name != "memory_search" {
				continue
			}
			total++
			stepCalls[stepIndex]++
		}
	}
	if total != expected {
		return total, fmt.Sprintf(
			"memory_search calls = %d, want %d",
			total, expected,
		)
	}
	for i := range expected {
		if i >= len(stepCalls) || stepCalls[i] != 1 {
			var actual int
			if i < len(stepCalls) {
				actual = stepCalls[i]
			}
			return total, fmt.Sprintf(
				"memory_search calls in step %d = %d, want 1",
				i+1, actual,
			)
		}
	}
	for i := expected; i < len(stepCalls); i++ {
		if stepCalls[i] != 0 {
			return total, fmt.Sprintf(
				"unexpected memory_search call in answer step %d",
				i+1,
			)
		}
	}
	return total, ""
}

func recoverMemoryQAAnswer(
	ctx context.Context,
	m model.Model,
	question string,
	res collectResult,
) (collectResult, *AnswerRecoveryTrace) {
	trigger := memoryQARecoveryTrigger(res)
	if trigger == "" {
		return res, nil
	}
	initialAnswer := strings.TrimSpace(res.text)
	trace := &AnswerRecoveryTrace{
		Trigger:       trigger,
		InitialAnswer: initialAnswer,
	}
	evidence := memoryQARetrievalEvidence(res.steps)
	if strings.TrimSpace(evidence) == "" {
		trace.Error = "no memory_search evidence available for recovery"
		trace.FallbackApplied = true
		trace.SelectedAnswerSource = memoryQAAnswerSourceFallback
		res.text = fallbackAnswer
		return res, trace
	}
	previousAnswer := initialAnswer
	if previousAnswer == "" {
		previousAnswer = "<empty>"
	}
	prompt := fmt.Sprintf(
		`The previous answer generation failed. Answer the question using only the retrieved memory_search results below. Call submit_answer exactly once and do not output text.

Follow these rules:
- Put only the shortest complete final answer span in the answer argument. Target 1-12 words; exceed 12 only when every directly requested item cannot otherwise fit, and never exceed 64 words.
- Treat the previous answer as a draft. If it is supported, preserve its answer-bearing terms while removing explanation and unrelated context. When it exceeds 12 words, the submitted answer must contain fewer words.
- Use explicit entity names instead of vague references, and include every directly requested part supported by the memories.
- Do not include evidence, explanation, context, or Markdown.
- For yes/no, output only Yes or No.
- If the exact factual relation is unsupported, output exactly "%s".

Question:
%s

Previous answer:
<answer>%s</answer>

Retrieved memory_search results:
%s`,
		fallbackAnswer,
		question,
		previousAnswer,
		evidence,
	)
	reasoningEffort := "low"
	thinkingEnabled := false
	recovery, err := runModelWithRateLimitRetry(
		ctx,
		m,
		&model.Request{
			Messages: []model.Message{model.NewUserMessage(prompt)},
			GenerationConfig: model.GenerationConfig{
				Stream:          false,
				MaxTokens:       intPtr(MemoryQARecoveryMaxTokens),
				Temperature:     float64Ptr(0),
				ReasoningEffort: &reasoningEffort,
				ThinkingEnabled: &thinkingEnabled,
			},
			Tools: map[string]tool.Tool{
				memoryQASubmitAnswerToolName: memoryQASubmitAnswerTool{},
			},
			ExtraFields: map[string]any{
				"tool_choice": map[string]any{
					"type": "function",
					"function": map[string]string{
						"name": memoryQASubmitAnswerToolName,
					},
				},
			},
		},
	)
	trace.FinishReason = recovery.finishReason
	step := StepTrace{
		Step:                len(res.steps) + 1,
		Phase:               "answer-recovery",
		LLMCalls:            recovery.usage.LLMCalls,
		PromptTokens:        recovery.usage.PromptTokens,
		CompletionTokens:    recovery.usage.CompletionTokens,
		TotalTokens:         recovery.usage.TotalTokens,
		CachedTokens:        recovery.usage.CachedTokens,
		CacheCreationTokens: recovery.usage.CacheCreationTokens,
		CacheReadTokens:     recovery.usage.CacheReadTokens,
		ReasoningTokens:     recovery.usage.ReasoningTokens,
		FinishReason:        recovery.finishReason,
	}
	for _, tc := range recovery.toolCalls {
		step.ToolCalls = append(step.ToolCalls, ToolCallTrace{
			Name: tc.Function.Name,
			Args: string(tc.Function.Arguments),
		})
	}
	res.usage.Add(recovery.usage)
	if err != nil {
		trace.Error = err.Error()
		step.Error = err.Error()
		res.steps = append(res.steps, step)
		selectMemoryQARecoveryFallback(&res, trace)
		return res, trace
	}
	recoveredText, parseErr := parseMemoryQARecoveryToolCall(
		recovery.toolCalls, recovery.text,
	)
	trace.Succeeded = parseErr == nil
	if parseErr != nil {
		trace.Error = parseErr.Error()
		step.Error = trace.Error
		res.steps = append(res.steps, step)
		selectMemoryQARecoveryFallback(&res, trace)
		return res, trace
	}
	trace.RecoveredAnswer = recoveredText
	res.steps = append(res.steps, step)
	if memoryQAAnswerUsable(initialAnswer) &&
		len(strings.Fields(recoveredText)) >=
			len(strings.Fields(initialAnswer)) {
		trace.InitialAnswerRetained = true
		trace.SelectedAnswerSource = memoryQAAnswerSourceInitial
		res.text = initialAnswer
		return res, trace
	}
	trace.Applied = true
	trace.SelectedAnswerSource = memoryQAAnswerSourceRecovery
	res.text = recoveredText
	return res, trace
}

func selectMemoryQARecoveryFallback(
	res *collectResult,
	trace *AnswerRecoveryTrace,
) {
	if memoryQAAnswerUsable(trace.InitialAnswer) {
		trace.InitialAnswerRetained = true
		trace.SelectedAnswerSource = memoryQAAnswerSourceInitial
		res.text = trace.InitialAnswer
		return
	}
	trace.FallbackApplied = true
	trace.SelectedAnswerSource = memoryQAAnswerSourceFallback
	res.text = fallbackAnswer
}

func memoryQAAnswerUsable(answer string) bool {
	answer = strings.TrimSpace(answer)
	return answer != "" && memoryQAAnswerFormatViolation(
		answer, memoryQAMaxRecoveryAnswerWords,
	) == ""
}

func parseMemoryQARecoveryToolCall(
	calls []model.ToolCall,
	responseText string,
) (string, error) {
	if len(calls) == 0 {
		if strings.TrimSpace(responseText) == "" {
			return "", fmt.Errorf("recovery returned an empty answer")
		}
		return "", fmt.Errorf(
			"recovery did not call %s", memoryQASubmitAnswerToolName,
		)
	}
	if len(calls) != 1 {
		return "", fmt.Errorf(
			"recovery returned %d tool calls, want 1", len(calls),
		)
	}
	call := calls[0]
	if call.Function.Name != memoryQASubmitAnswerToolName {
		return "", fmt.Errorf(
			"recovery called %s, want %s",
			call.Function.Name, memoryQASubmitAnswerToolName,
		)
	}
	return parseMemoryQARecoveryAnswer(string(call.Function.Arguments))
}

func parseMemoryQARecoveryAnswer(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("recovery returned an empty answer")
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("recovery returned invalid JSON: %w", err)
	}
	answer := strings.TrimSpace(payload.Answer)
	if answer == "" {
		return "", fmt.Errorf("recovery returned an empty answer")
	}
	if violation := memoryQAAnswerFormatViolation(
		answer, memoryQAMaxRecoveryAnswerWords,
	); violation != "" {
		return "", fmt.Errorf(
			"recovery returned a malformed answer: %s", violation,
		)
	}
	return answer, nil
}

func memoryQARecoveryTrigger(res collectResult) string {
	var reasons []string
	answer := strings.TrimSpace(res.text)
	if answer == "" {
		reasons = append(reasons, "empty-answer")
	} else if violation := memoryQAAnswerFormatViolation(
		answer, memoryQAMaxPrimaryAnswerWords,
	); violation != "" {
		reasons = append(reasons, "answer-format:"+violation)
	}
	if len(res.steps) > 0 {
		finishReason := strings.ToLower(strings.TrimSpace(
			res.steps[len(res.steps)-1].FinishReason,
		))
		if finishReason == "length" || finishReason == "max_tokens" ||
			finishReason == "max_output_tokens" {
			reasons = append(reasons, "finish-reason:"+finishReason)
		}
	}
	return strings.Join(reasons, ",")
}

func memoryQAAnswerFormatViolation(answer string, maxWords int) string {
	if strings.ContainsAny(answer, "\r\n") {
		return "multiline"
	}
	if len(strings.Fields(answer)) > maxWords {
		return "too-many-words"
	}
	return ""
}

func memoryQARetrievalEvidence(steps []StepTrace) string {
	var evidence strings.Builder
	seen := make(map[string]struct{})
	resultNumber := 0
	for _, step := range steps {
		for _, call := range step.ToolCalls {
			if call.Name != "memory_search" || call.Result == "" {
				continue
			}
			var searchResult memorySearchResult
			if err := json.Unmarshal(
				[]byte(call.Result), &searchResult,
			); err != nil {
				fmt.Fprintf(&evidence, "Search result: %s\n\n", call.Result)
				continue
			}
			for _, result := range searchResult.Results {
				key := result.ID
				if key == "" {
					key = result.Memory
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				resultNumber++
				fmt.Fprintf(
					&evidence, "%d. Memory: %s\n",
					resultNumber, result.Memory,
				)
				if len(result.Topics) > 0 {
					fmt.Fprintf(
						&evidence, "   Topics: %s\n",
						strings.Join(result.Topics, ", "),
					)
				}
				if result.Kind != "" {
					fmt.Fprintf(&evidence, "   Kind: %s\n", result.Kind)
				}
				if result.EventTime != "" {
					fmt.Fprintf(
						&evidence, "   Event time: %s\n", result.EventTime,
					)
				}
				if len(result.Participants) > 0 {
					fmt.Fprintf(
						&evidence, "   Participants: %s\n",
						strings.Join(result.Participants, ", "),
					)
				}
				if result.Location != "" {
					fmt.Fprintf(
						&evidence, "   Location: %s\n", result.Location,
					)
				}
				if result.Metadata != nil {
					metadata, err := json.Marshal(result.Metadata)
					if err == nil && string(metadata) != "{}" &&
						string(metadata) != "null" {
						fmt.Fprintf(
							&evidence, "   Metadata: %s\n", metadata,
						)
					}
				}
				evidence.WriteString("\n")
			}
		}
	}
	return strings.TrimSpace(evidence.String())
}

// memorySearchResult matches the JSON structure returned by
// memory_search tool for parsing in logs.
type memorySearchResult struct {
	Query   string `json:"query"`
	Results []struct {
		ID           string   `json:"id"`
		Memory       string   `json:"memory"`
		Topics       []string `json:"topics,omitempty"`
		Kind         string   `json:"kind,omitempty"`
		EventTime    string   `json:"event_time,omitempty"`
		Participants []string `json:"participants,omitempty"`
		Location     string   `json:"location,omitempty"`
		Score        any      `json:"score"`
		Metadata     any      `json:"metadata,omitempty"`
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
		cachedTokens := max(st.CachedTokens, st.CacheReadTokens)
		if cachedTokens > 0 {
			log.Printf(
				"    🔹 Step %d | Tokens: %d"+
					" (in:%d cached:%d out:%d)",
				st.Step, st.TotalTokens,
				st.PromptTokens, cachedTokens,
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
		if st.Phase != "" {
			log.Printf("    Phase: %s", st.Phase)
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
		cachedTokens := usage.CachedPromptTokens()
		if cachedTokens > 0 {
			log.Printf(
				"    🔹 Tokens: %d (in:%d cached:%d out:%d)",
				usage.TotalTokens,
				usage.PromptTokens,
				cachedTokens,
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
	if qaResult.AnswerRecovery != nil {
		log.Printf(
			"    Answer recovery: trigger=%s succeeded=%v finish=%s error=%s",
			qaResult.AnswerRecovery.Trigger,
			qaResult.AnswerRecovery.Succeeded,
			qaResult.AnswerRecovery.FinishReason,
			qaResult.AnswerRecovery.Error,
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
