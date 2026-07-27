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
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	autoAppName = "memory-eval-auto"

	autoQAMaxToolIterations = 10
)

// AutoEvaluator evaluates using automatic memory extraction.
// Memories are extracted by Runner and stored by the memory service.
type AutoEvaluator struct {
	model         model.Model
	evalModel     model.Model
	memoryService memory.Service
	config        Config
	llmJudge      *metrics.LLMJudge
}

// NewAutoEvaluator creates a new auto evaluator.
func NewAutoEvaluator(
	m, evalModel model.Model,
	memSvc memory.Service,
	cfg Config,
) *AutoEvaluator {
	e := &AutoEvaluator{
		model:         m,
		evalModel:     evalModel,
		memoryService: memSvc,
		config:        cfg,
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *AutoEvaluator) Name() string {
	return "auto"
}

// Evaluate runs evaluation on a sample using runner-triggered
// auto extraction.
func (e *AutoEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{
		AppName: autoAppName, UserID: sample.SampleID,
	}
	if e.config.ExtractionTracker != nil {
		e.config.ExtractionTracker.SnapshotWithCalls()
	}
	if e.config.SnapshotEmbeddingUsage != nil {
		e.config.SnapshotEmbeddingUsage()
	}

	if e.config.ReuseMemories {
		if err := e.requireExistingMemories(ctx, userKey); err != nil {
			return e.failedExtractionResult(startTime, sample), err
		}
	} else {
		if err := e.seedMemories(ctx, userKey, sample); err != nil {
			return e.failedExtractionResult(startTime, sample), err
		}
	}
	var extractionUsage TokenUsage
	var extractionCalls []ExtractionCallTrace
	if e.config.ExtractionTracker != nil {
		extractionUsage, extractionCalls =
			e.config.ExtractionTracker.SnapshotWithCalls()
	}
	var extractionEmbeddingUsage EmbeddingUsage
	if e.config.SnapshotEmbeddingUsage != nil {
		extractionEmbeddingUsage = e.config.SnapshotEmbeddingUsage()
	}

	// Phase 2: Answer questions via agent with memory_search.
	qaMemSvc := &noAutoMemoryService{inner: e.memoryService}
	qaAgent := newAutoQAAgent(
		e.model,
		qaMemSvc.Tools(),
		e.config.QASearchPasses,
	)
	qaRunner := runner.NewRunner(
		autoAppName,
		qaAgent,
		runner.WithSessionService(newSessionService(e.config)),
		runner.WithMemoryService(qaMemSvc),
	)
	defer qaRunner.Close()

	result := &SampleResult{SampleID: sample.SampleID}
	result.QAResults = make([]*QAResult, 0, len(sample.QA))
	catAgg := metrics.NewCategoryAggregator()
	var sampleUsage TokenUsage

	historyMsgs := buildHistoryMessages(
		sample, e.config.QAHistoryTurns,
	)

	for i, qa := range sample.QA {
		qaResult, err := e.evaluateQA(
			ctx, qaRunner, userKey, qa, historyMsgs,
		)
		if err != nil {
			if e.config.Verbose {
				log.Printf(
					"Warning: evaluate QA %s failed: %v",
					qa.QuestionID, err,
				)
			}
			qaResult = qaResultFromError(qa, err)
		}
		if e.config.Verbose {
			logVerboseQAResult(i, len(sample.QA), qa, qaResult)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
		if qaResult.TokenUsage != nil {
			sampleUsage.Add(*qaResult.TokenUsage)
		}
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	result.QATokenUsage = tokenUsagePointer(sampleUsage)
	result.ExtractionTokenUsage = tokenUsagePointer(extractionUsage)
	result.ExtractionCalls = extractionCalls
	totalUsage := extractionUsage
	totalUsage.Add(sampleUsage)
	result.TokenUsage = tokenUsagePointer(totalUsage)

	var qaEmbeddingUsage EmbeddingUsage
	if e.config.SnapshotEmbeddingUsage != nil {
		qaEmbeddingUsage = e.config.SnapshotEmbeddingUsage()
	}
	result.ExtractionEmbeddingUsage = embeddingUsagePointer(
		extractionEmbeddingUsage,
	)
	result.QAEmbeddingUsage = embeddingUsagePointer(qaEmbeddingUsage)
	totalEmbeddingUsage := extractionEmbeddingUsage
	totalEmbeddingUsage.Add(qaEmbeddingUsage)
	result.EmbeddingUsage = embeddingUsagePointer(totalEmbeddingUsage)
	return result, nil
}

func tokenUsagePointer(usage TokenUsage) *TokenUsage {
	if usage.IsZero() {
		return nil
	}
	return &usage
}

func embeddingUsagePointer(usage EmbeddingUsage) *EmbeddingUsage {
	if usage.IsZero() {
		return nil
	}
	return &usage
}

func newAutoQAAgent(
	m model.Model,
	tools []tool.Tool,
	searchPasses int,
) agent.Agent {
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(
			qaMemorySearchInstruction(searchPasses),
		),
		llmagent.WithGenerationConfig(memoryQAGenerationConfig()),
		llmagent.WithTools(tools),
		llmagent.WithMaxToolIterations(autoQAMaxToolIterations),
	)
}

func (e *AutoEvaluator) requireExistingMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	entries, err := e.memoryService.ReadMemories(ctx, userKey, 1)
	if err != nil {
		return fmt.Errorf("read reused memories: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf(
			"reuse memories requested but no memories exist for sample %s",
			userKey.UserID,
		)
	}
	return nil
}

func (e *AutoEvaluator) seedMemories(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	if err := e.memoryService.ClearMemories(ctx, userKey); err != nil {
		return fmt.Errorf("clear memories: %w", err)
	}

	sessionSvc := newSessionService(e.config)
	seedMemSvc := &noAutoMemoryService{inner: e.memoryService}
	seedRunner := runner.NewRunner(
		autoAppName,
		seedAgent{},
		runner.WithSessionService(sessionSvc),
		runner.WithMemoryService(seedMemSvc),
	)
	defer seedRunner.Close()

	seeds := make([]autoExtractionSeed, 0, len(sample.Conversation))
	for _, sess := range sample.Conversation {
		sessionID := fmt.Sprintf("seed-%s", sess.SessionID)
		msgs := sessionMessages(sess, sample.Speakers)
		seedCtx := ctx
		if t, ok := parseSessionDate(sess.SessionDate); ok {
			seedCtx = extractor.WithReferenceDate(seedCtx, t)
		}
		ch, err := runner.RunWithMessages(
			seedCtx, seedRunner,
			userKey.UserID, sessionID, msgs,
		)
		if err != nil {
			return fmt.Errorf("seed session %s: %w", sess.SessionID, err)
		}
		if _, err := collectFinalText(ch); err != nil {
			return fmt.Errorf("seed session %s: %w", sess.SessionID, err)
		}
		key := session.Key{
			AppName:   autoAppName,
			UserID:    userKey.UserID,
			SessionID: sessionID,
		}
		seededSession, err := sessionSvc.GetSession(seedCtx, key)
		if err != nil {
			return fmt.Errorf("get seed session %s: %w", sess.SessionID, err)
		}
		if seededSession == nil {
			return fmt.Errorf("get seed session %s: not found", sess.SessionID)
		}
		seeds = append(seeds, autoExtractionSeed{
			ctx:     seedCtx,
			session: seededSession,
		})
	}

	if err := enqueueAutoExtractionsSequentially(
		ctx,
		e.memoryService,
		seeds,
		autoExtractionWaitTimeout(
			len(seeds), e.config.AutoExtractionTimeout,
		),
	); err != nil {
		return err
	}
	return nil
}

type autoExtractionSeed struct {
	ctx     context.Context
	session *session.Session
}

type autoExtractionEnqueuer interface {
	EnqueueAutoMemoryJob(context.Context, *session.Session) error
}

func enqueueAutoExtractionsSequentially(
	ctx context.Context,
	service autoExtractionEnqueuer,
	seeds []autoExtractionSeed,
	timeout time.Duration,
) error {
	if len(seeds) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = autoExtractionWaitTimeout(len(seeds), 0)
	}
	deadline := time.Now().Add(timeout)
	for index, seed := range seeds {
		if seed.session == nil {
			return fmt.Errorf("seed session %d is nil", index)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("auto extraction timeout after %s", timeout)
		}
		seedCtx := seed.ctx
		if seedCtx == nil {
			seedCtx = ctx
		}
		if err := service.EnqueueAutoMemoryJob(
			seedCtx, seed.session,
		); err != nil {
			return fmt.Errorf(
				"enqueue seed session %s: %w", seed.session.ID, err,
			)
		}
		if err := waitForAutoExtraction(
			ctx, []*session.Session{seed.session}, remaining,
		); err != nil {
			return fmt.Errorf("wait for auto extraction: %w", err)
		}
	}
	return nil
}

func (e *AutoEvaluator) evaluateQA(
	ctx context.Context,
	r runner.Runner,
	userKey memory.UserKey,
	qa dataset.QAItem,
	historyMsgs []model.Message,
) (*QAResult, error) {
	start := time.Now()
	sessionID := fmt.Sprintf("qa-%s", qa.QuestionID)
	msg := memoryQAUserMessage(qa.Question)

	var runOpts []agent.RunOption
	if len(historyMsgs) > 0 {
		runOpts = append(runOpts,
			agent.WithInjectedContextMessages(historyMsgs),
		)
	}

	res, err := runWithRateLimitRetry(
		ctx, func() (<-chan *event.Event, error) {
			return r.Run(
				ctx, userKey.UserID, sessionID, msg,
				runOpts...,
			)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	res, answerRecovery := recoverMemoryQAAnswer(
		ctx, e.model, qa.Question, res,
	)
	predicted := res.text

	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(
			ctx, qa.Question, qa.Answer, predicted,
		)
		if err == nil {
			if judgeResult.Correct {
				m.LLMScore = judgeResult.Confidence
			} else {
				m.LLMScore = 0
			}
		}
	}

	tu := res.usage
	searchCalls, protocolError := memorySearchProtocol(
		res.steps, e.config.QASearchPasses,
	)
	return &QAResult{
		QuestionID:     qa.QuestionID,
		Question:       qa.Question,
		Category:       qa.Category,
		Expected:       qa.Answer,
		Predicted:      predicted,
		Metrics:        m,
		LatencyMs:      time.Since(start).Milliseconds(),
		TokenUsage:     &tu,
		Steps:          res.steps,
		SearchCalls:    searchCalls,
		ProtocolError:  protocolError,
		AnswerRecovery: answerRecovery,
	}, nil
}

func qaResultFromError(qa dataset.QAItem, err error) *QAResult {
	_ = err
	m := metrics.QAMetrics{F1: 0, BLEU: 0}
	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  fallbackAnswer,
		Metrics:    m,
	}
}

const autoMemoryLastErrorStateKey = "memory:last_extract_error"

func autoExtractionWaitTimeout(
	sessionCount int,
	configured time.Duration,
) time.Duration {
	if configured > 0 {
		return configured
	}
	return min(
		max(time.Duration(sessionCount)*time.Minute, 5*time.Minute),
		60*time.Minute,
	)
}

func (e *AutoEvaluator) failedExtractionResult(
	startedAt time.Time,
	sample *dataset.LoCoMoSample,
) *SampleResult {
	result := &SampleResult{
		SampleID:    sample.SampleID,
		QAResults:   []*QAResult{},
		ByCategory:  map[string]metrics.CategoryMetrics{},
		TotalTimeMs: time.Since(startedAt).Milliseconds(),
	}
	if e.config.ExtractionTracker != nil {
		usage, calls := e.config.ExtractionTracker.SnapshotWithCalls()
		result.ExtractionTokenUsage = tokenUsagePointer(usage)
		result.TokenUsage = tokenUsagePointer(usage)
		result.ExtractionCalls = calls
	}
	if e.config.SnapshotEmbeddingUsage != nil {
		usage := e.config.SnapshotEmbeddingUsage()
		result.ExtractionEmbeddingUsage = embeddingUsagePointer(usage)
		result.EmbeddingUsage = embeddingUsagePointer(usage)
	}
	return result
}

func waitForAutoExtraction(
	ctx context.Context,
	sessions []*session.Session,
	timeout time.Duration,
) error {
	if len(sessions) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = autoExtractionWaitTimeout(len(sessions), 0)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	wants := make([]time.Time, len(sessions))
	for i, sess := range sessions {
		if sess == nil {
			return fmt.Errorf("session %d is nil", i)
		}
		wants[i] = latestAutoExtractionTimestamp(sess)
		if wants[i].IsZero() {
			return fmt.Errorf("session %s has no events to extract", sess.ID)
		}
	}
	for {
		allComplete := true
		for i, sess := range sessions {
			if raw, ok := sess.GetState(autoMemoryLastErrorStateKey); ok &&
				len(raw) > 0 {
				return fmt.Errorf("session %s: %s", sess.ID, raw)
			}
			raw, ok := sess.GetState(
				memory.SessionStateKeyAutoMemoryLastExtractAt,
			)
			if !ok {
				allComplete = false
				continue
			}
			got, parseErr := time.Parse(time.RFC3339Nano, string(raw))
			if parseErr != nil {
				return fmt.Errorf(
					"session %s completion marker %q: %w",
					sess.ID, raw, parseErr,
				)
			}
			if got.Before(wants[i]) {
				allComplete = false
			}
		}
		if allComplete {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("auto extraction timeout after %s", timeout)
		case <-ticker.C:
		}
	}
}

func latestAutoExtractionTimestamp(sess *session.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	events := sess.GetEvents()
	var latest time.Time
	for _, event := range events {
		if event.Timestamp.After(latest) {
			latest = event.Timestamp
		}
	}
	return latest.UTC()
}
