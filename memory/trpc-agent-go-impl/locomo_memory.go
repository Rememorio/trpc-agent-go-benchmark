//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// LoCoMo support evaluates long-term conversational memory using the LoCoMo
// benchmark scenarios.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go-benchmark/memory/trpc-agent-go-impl/evaluation/scenarios"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorymysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	memorypgvector "trpc.group/trpc-go/trpc-agent-go/memory/pgvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionpgvector "trpc.group/trpc-go/trpc-agent-go/session/pgvector"
)

const (
	pgvectorTableDefaultBase  = "memory_eval"
	pgvectorTableAutoBase     = "memory_eval_auto"
	mysqlTableDefaultBase     = "memory_eval_mysql"
	mysqlTableAutoBase        = "memory_eval_auto_mysql"
	sqliteTableDefaultBase    = "memory_eval_sqlite"
	sqliteTableAutoBase       = "memory_eval_auto_sqlite"
	sqliteVecTableDefaultBase = "memory_eval_sqlitevec"
	sqliteVecTableAutoBase    = "memory_eval_auto_sqlitevec"
	sessionRecallTableBase    = "session_eval_recall"

	autoMemoryAsyncWorkers      = 3
	autoMemoryQueueSize         = 200
	defaultAutoMemoryJobTimeout = 2 * time.Minute
)

const (
	memoryModeNone   memoryMode = "none"
	memoryModeManual memoryMode = "manual"
	memoryModeAuto   memoryMode = "auto"
)

type memoryMode string

type memoryConfig struct {
	backend string
	mode    memoryMode
}

// EvaluationResult holds the complete evaluation result.
type EvaluationResult struct {
	Metadata      *EvalMetadata                      `json:"metadata"`
	Summary       *EvalSummary                       `json:"summary"`
	ByCategory    map[string]metrics.CategoryMetrics `json:"by_category"`
	SampleResults []*scenarios.SampleResult          `json:"sample_results,omitempty"`
	Failures      []EvaluationFailure                `json:"failures,omitempty"`
}

// EvaluationFailure preserves failed-sample diagnostics and incurred cost.
type EvaluationFailure struct {
	SampleID                 string                          `json:"sample_id"`
	Error                    string                          `json:"error"`
	TotalTimeMs              int64                           `json:"total_time_ms"`
	TokenUsage               *scenarios.TokenUsage           `json:"token_usage,omitempty"`
	ExtractionTokenUsage     *scenarios.TokenUsage           `json:"extraction_token_usage,omitempty"`
	QATokenUsage             *scenarios.TokenUsage           `json:"qa_token_usage,omitempty"`
	EmbeddingUsage           *scenarios.EmbeddingUsage       `json:"embedding_usage,omitempty"`
	ExtractionEmbeddingUsage *scenarios.EmbeddingUsage       `json:"extraction_embedding_usage,omitempty"`
	QAEmbeddingUsage         *scenarios.EmbeddingUsage       `json:"qa_embedding_usage,omitempty"`
	ExtractionCalls          []scenarios.ExtractionCallTrace `json:"extraction_calls,omitempty"`
}

// EvalMetadata holds evaluation metadata.
type EvalMetadata struct {
	Framework             string                    `json:"framework"`
	Version               string                    `json:"version"`
	Timestamp             time.Time                 `json:"timestamp"`
	Model                 string                    `json:"model"`
	ModelVariant          string                    `json:"model_variant,omitempty"`
	EvalModel             string                    `json:"eval_model,omitempty"`
	Scenario              string                    `json:"scenario"`
	MemoryBackend         string                    `json:"memory_backend,omitempty"`
	MaxContext            int                       `json:"max_context"`
	QAHistoryTurns        int                       `json:"qa_history_turns,omitempty"`
	QASearchPasses        int                       `json:"qa_search_passes,omitempty"`
	QAPromptVersion       string                    `json:"qa_prompt_version,omitempty"`
	QASearchStrategy      string                    `json:"qa_search_strategy,omitempty"`
	QARecoveryMaxTokens   int                       `json:"qa_recovery_max_tokens,omitempty"`
	VectorTopK            int                       `json:"vector_topk,omitempty"`
	ReplayProtocol        string                    `json:"replay_protocol,omitempty"`
	RoleMapping           string                    `json:"role_mapping,omitempty"`
	TokenUsageScope       string                    `json:"token_usage_scope,omitempty"`
	EmbeddingUsageScope   string                    `json:"embedding_usage_scope,omitempty"`
	ReuseMemories         bool                      `json:"reuse_memories"`
	AutoExtractionTimeout string                    `json:"auto_extraction_timeout,omitempty"`
	AutoMemoryJobTimeout  string                    `json:"auto_memory_job_timeout,omitempty"`
	TableSuffix           string                    `json:"table_suffix,omitempty"`
	PGVectorExtraction    *pgvectorExtractionConfig `json:"pgvector_extraction,omitempty"`
	Build                 lmeBuildProvenance        `json:"build"`
	LLMJudge              bool                      `json:"llm_judge"`
}

// EvalSummary holds aggregated evaluation summary.
type EvalSummary struct {
	TotalSamples    int     `json:"total_samples"`
	FailedSamples   int     `json:"failed_samples,omitempty"`
	TotalQuestions  int     `json:"total_questions"`
	OverallF1       float64 `json:"overall_f1"`
	OverallBLEU     float64 `json:"overall_bleu"`
	OverallLLMScore float64 `json:"overall_llm_score,omitempty"`
	TotalTimeMs     int64   `json:"total_time_ms"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`

	// Token usage statistics.
	TotalPromptTokens       int     `json:"total_prompt_tokens"`
	TotalCompletionTokens   int     `json:"total_completion_tokens"`
	TotalTokens             int     `json:"total_tokens"`
	TotalCachedTokens       int     `json:"total_cached_tokens,omitempty"`
	TotalLLMCalls           int     `json:"total_llm_calls"`
	ProtocolViolations      int     `json:"protocol_violations"`
	AnswerRecoveryAttempts  int     `json:"answer_recovery_attempts"`
	AnswerRecoverySuccesses int     `json:"answer_recovery_successes"`
	AnswerRecoveryApplied   int     `json:"answer_recovery_applied"`
	AnswerRecoveryRetained  int     `json:"answer_recovery_initial_retained"`
	AnswerRecoveryFallbacks int     `json:"answer_recovery_fallbacks"`
	AvgPromptTokensPerQA    float64 `json:"avg_prompt_tokens_per_qa"`
	AvgCompletionPerQA      float64 `json:"avg_completion_tokens_per_qa"`
	AvgCachedTokensPerQA    float64 `json:"avg_cached_tokens_per_qa,omitempty"`
	AvgLLMCallsPerQA        float64 `json:"avg_llm_calls_per_qa"`
	// CacheHitRate is the fraction of prompt tokens served
	// from the provider's prompt cache (0.0–1.0).
	CacheHitRate float64 `json:"cache_hit_rate,omitempty"`

	TokenUsage               *scenarios.TokenUsage     `json:"token_usage,omitempty"`
	ExtractionTokenUsage     *scenarios.TokenUsage     `json:"extraction_token_usage,omitempty"`
	QATokenUsage             *scenarios.TokenUsage     `json:"qa_token_usage,omitempty"`
	EmbeddingUsage           *scenarios.EmbeddingUsage `json:"embedding_usage,omitempty"`
	ExtractionEmbeddingUsage *scenarios.EmbeddingUsage `json:"extraction_embedding_usage,omitempty"`
	QAEmbeddingUsage         *scenarios.EmbeddingUsage `json:"qa_embedding_usage,omitempty"`
}

const (
	locomoAutoReplayProtocol = "chronological-session-batch-auto-v2"
	locomoRoleMapping        = "session-opening human speaker=user; other human speaker=assistant; " +
		"speaker names retained in message content"
)

func runLoCoMoMemory(ctx context.Context) error {
	modelName := getModelName()
	modelVariant := getModelVariant()
	evalModelName := getEvalModelName()
	outputDir := *flagOutput
	scenariosToRun := getScenarios(*flagScenario)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	// Parse memory backends.
	backends := parseMemoryBackends(*flagMemoryBackends)

	log.Printf("=== Memory Evaluation (LoCoMo Benchmark) ===")
	log.Printf("Model: %s", modelName)
	if modelVariant != "" {
		log.Printf("Model Variant: %s", modelVariant)
	}
	log.Printf("Eval Model: %s", evalModelName)
	log.Printf("Scenario: %s", *flagScenario)
	log.Printf("LLM Judge: %v", *flagLLMJudge)
	logScenarioConfig(scenariosToRun, backends)
	log.Printf("Output: %s", outputDir)
	if *flagTableSuffix != "" {
		log.Printf("Table Suffix: %s", *flagTableSuffix)
	}
	if *flagResume {
		log.Printf("Resume mode: enabled (checkpoint will be loaded if exists)")
	}

	// Load dataset.
	loader := dataset.NewLoader(*flagDataset)
	samples, err := loader.LoadSamples(*flagDataFile)
	if err != nil {
		return fmt.Errorf("load dataset: %w", err)
	}
	log.Printf("Loaded %d samples", len(samples))

	// Filter samples if specified.
	samples, err = filterSamples(samples)
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return fmt.Errorf("no samples to evaluate")
	}

	// Apply max tasks limit.
	if *flagMaxTasks > 0 && *flagMaxTasks < len(samples) {
		samples = samples[:*flagMaxTasks]
		log.Printf("Limited to %d samples", len(samples))
	}

	// Create models.
	llm, err := newEvaluationModel(modelName, modelVariant)
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}
	evalLLM := llm
	if evalModelName != "" && evalModelName != modelName {
		evalLLM, err = newEvaluationModel(evalModelName, modelVariant)
		if err != nil {
			return fmt.Errorf("create evaluation model: %w", err)
		}
	}

	// Base scenario config.
	baseConfig := scenarios.Config{
		MaxContext:            *flagMaxContext,
		EnableLLMJudge:        *flagLLMJudge,
		Verbose:               *flagVerbose,
		SessionEventLimit:     *flagSessionEventLimit,
		QAHistoryTurns:        *flagQAHistoryTurns,
		QASearchPasses:        *flagQASearchPasses,
		ReuseMemories:         *flagLoCoMoReuseMemories,
		AutoExtractionTimeout: *flagAutoExtractionTimeout,
		SessionRecallResults:  *flagVectorTopK,
		SessionRecallMinScore: *flagSessionRecallMinScore,
		DebugDumpMemories:     *flagDebugDumpMemories,
		DebugMemLimit:         *flagDebugMemLimit,
		DebugQALimit:          *flagDebugQALimit,
	}

	// Run evaluation for each scenario and backend combination.
	for _, scenarioType := range scenariosToRun {
		// Long-context doesn't need memory backends.
		if scenarioType == scenarios.ScenarioLongContext {
			if err := runScenario(
				ctx, samples, llm, evalLLM, scenarioType, "", baseConfig, outputDir,
			); err != nil {
				return err
			}
			continue
		}
		if scenarioType == scenarios.ScenarioSessionRecall {
			if err := runScenario(
				ctx,
				samples,
				llm,
				evalLLM,
				scenarioType,
				"session_pgvector",
				baseConfig,
				outputDir,
			); err != nil {
				return err
			}
			continue
		}

		// Run each backend for memory-based scenarios.
		for _, backend := range backends {
			if err := runScenario(
				ctx, samples, llm, evalLLM, scenarioType, backend, baseConfig, outputDir,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func logScenarioConfig(
	scenariosToRun []scenarios.ScenarioType,
	backends []string,
) {
	hasMemoryScenarios := containsScenario(
		scenariosToRun,
		scenarios.ScenarioAgentic,
	) || containsScenario(
		scenariosToRun,
		scenarios.ScenarioAuto,
	)
	if containsScenario(scenariosToRun, scenarios.ScenarioLongContext) {
		log.Printf("Context Mode: long_context transcript preload")
	}
	if containsScenario(scenariosToRun, scenarios.ScenarioSessionRecall) {
		log.Printf("Session Backend: session_pgvector")
		log.Printf("Session Recall Results: %d", *flagVectorTopK)
		if *flagSessionRecallMinScore > 0 {
			log.Printf(
				"Session Recall Min Score: %.3f",
				*flagSessionRecallMinScore,
			)
		}
	}
	if !hasMemoryScenarios {
		return
	}
	log.Printf("Memory Backends: %v", backends)
	if *flagQAHistoryTurns > 0 {
		log.Printf("QA History Turns: %d", *flagQAHistoryTurns)
	}
	if *flagLoCoMoReuseMemories {
		log.Printf("Reuse Memories: enabled (QA only)")
	}
	if *flagAutoExtractionTimeout > 0 &&
		containsScenario(scenariosToRun, scenarios.ScenarioAuto) {
		log.Printf("Auto Extraction Timeout: %s", *flagAutoExtractionTimeout)
	}
	if *flagQASearchPasses > 1 {
		log.Printf("QA Search Passes: %d", *flagQASearchPasses)
	}
}

func containsScenario(
	scenariosToRun []scenarios.ScenarioType,
	target scenarios.ScenarioType,
) bool {
	for _, scenario := range scenariosToRun {
		if scenario == target {
			return true
		}
	}
	return false
}

func getScenarios(scenario string) []scenarios.ScenarioType {
	scenarioMap := map[string]scenarios.ScenarioType{
		"long_context":   scenarios.ScenarioLongContext,
		"session_recall": scenarios.ScenarioSessionRecall,
		"agentic":        scenarios.ScenarioAgentic,
		"auto":           scenarios.ScenarioAuto,
	}
	if scenario == "all" {
		return []scenarios.ScenarioType{
			scenarios.ScenarioLongContext,
			scenarios.ScenarioSessionRecall,
			scenarios.ScenarioAgentic,
			scenarios.ScenarioAuto,
		}
	}
	// Support comma-separated scenarios.
	var result []scenarios.ScenarioType
	seen := make(map[string]bool)
	for _, s := range strings.Split(scenario, ",") {
		s = strings.TrimSpace(s)
		if seen[s] {
			continue
		}
		seen[s] = true
		st, ok := scenarioMap[s]
		if !ok {
			log.Fatalf("Invalid scenario: %s", s)
		}
		result = append(result, st)
	}
	return result
}

func filterSamples(
	samples []*dataset.LoCoMoSample,
) ([]*dataset.LoCoMoSample, error) {
	// Filter by sample ID.
	if *flagSampleID != "" {
		filtered := make([]*dataset.LoCoMoSample, 0)
		for _, s := range samples {
			if s.SampleID == *flagSampleID {
				filtered = append(filtered, s)
			}
		}
		samples = filtered
		log.Printf("Filtered to %d samples (sample_id=%s)", len(samples), *flagSampleID)
	}

	// Filter by category.
	if *flagCategory != "" {
		for _, s := range samples {
			filtered := make([]dataset.QAItem, 0)
			for _, qa := range s.QA {
				if qa.Category == *flagCategory {
					filtered = append(filtered, qa)
				}
			}
			s.QA = filtered
		}
		log.Printf("Filtered QA by category: %s", *flagCategory)
	}

	questionIDs := parseCommaList(*flagLoCoMoQuestionIDs)
	if len(questionIDs) > 0 {
		var err error
		samples, err = filterLoCoMoQuestions(samples, questionIDs)
		if err != nil {
			return nil, err
		}
		log.Printf(
			"Filtered LoCoMo QA to %d question IDs",
			len(questionIDs),
		)
	}
	return samples, nil
}

func filterLoCoMoQuestions(
	samples []*dataset.LoCoMoSample,
	questionIDs []string,
) ([]*dataset.LoCoMoSample, error) {
	wanted := make(map[string]struct{}, len(questionIDs))
	for _, id := range questionIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	found := make(map[string]struct{}, len(wanted))
	filteredSamples := make([]*dataset.LoCoMoSample, 0, len(samples))
	for _, sample := range samples {
		if sample == nil {
			continue
		}
		qaItems := make([]dataset.QAItem, 0, len(sample.QA))
		for _, qa := range sample.QA {
			if _, ok := wanted[qa.QuestionID]; !ok {
				continue
			}
			qaItems = append(qaItems, qa)
			found[qa.QuestionID] = struct{}{}
		}
		if len(qaItems) == 0 {
			continue
		}
		sample.QA = qaItems
		filteredSamples = append(filteredSamples, sample)
	}
	missing := make([]string, 0)
	for id := range wanted {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return nil, fmt.Errorf(
			"LoCoMo question IDs not found: %s",
			strings.Join(missing, ", "),
		)
	}
	return filteredSamples, nil
}

func runScenario(
	ctx context.Context,
	samples []*dataset.LoCoMoSample,
	llm, evalLLM model.Model,
	scenarioType scenarios.ScenarioType,
	backend string,
	baseConfig scenarios.Config,
	outputDir string,
) error {
	config := baseConfig
	config.Scenario = scenarioType

	var evaluator scenarios.Evaluator
	var memSvc memory.Service
	var sessionSvc session.Service
	var err error
	memCfg := buildMemoryConfig(scenarioType, backend)
	memOpts, configErr := buildMemoryServiceOptions(memCfg, llm)
	if configErr != nil {
		return fmt.Errorf(
			"configure %s memory service: %w", backend, configErr,
		)
	}
	if memOpts.enableExtractor {
		extractionTracker := scenarios.NewTokenTracker()
		config.ExtractionTracker = extractionTracker
		memOpts.extractorCallbacks =
			scenarios.NewModelCallbacksWithTracker(extractionTracker)
	}
	if scenarioType == scenarios.ScenarioAuto && backend == "pgvector" {
		embeddingTracker := newLongMemEvalTrackingEmbedder(
			newEmbeddingEmbedder(getEmbedModelName()),
		)
		memOpts.embeddingTracker = embeddingTracker
		config.SnapshotEmbeddingUsage = func() scenarios.EmbeddingUsage {
			usage := embeddingTracker.Snapshot()
			return scenarios.EmbeddingUsage{
				PromptTokens: usage.PromptTokens,
				TotalTokens:  usage.TotalTokens,
				Calls:        usage.Calls,
			}
		}
	}

	switch scenarioType {
	case scenarios.ScenarioLongContext:
		evaluator = scenarios.NewLongContextEvaluator(llm, evalLLM, config)
	case scenarios.ScenarioSessionRecall:
		sessionSvc, err = createSessionRecallService(config)
		if err != nil {
			return fmt.Errorf("create session recall service: %w", err)
		}
		evaluator = scenarios.NewSessionRecallEvaluator(
			llm, evalLLM, sessionSvc, config,
		)
	case scenarios.ScenarioAgentic:
		memSvc, err = createMemoryService(memCfg, memOpts)
		if err != nil {
			return fmt.Errorf("create %s memory service: %w", backend, err)
		}
		evaluator = scenarios.NewAgenticEvaluator(llm, evalLLM, memSvc, config)
	case scenarios.ScenarioAuto:
		memSvc, err = createMemoryService(memCfg, memOpts)
		if err != nil {
			return fmt.Errorf("create %s memory service: %w", backend, err)
		}
		evaluator = scenarios.NewAutoEvaluator(llm, evalLLM, memSvc, config)
	}

	// Determine output directory.
	scenarioDir := buildScenarioDir(outputDir, scenarioType, backend)
	if err := os.MkdirAll(scenarioDir, 0755); err != nil {
		return fmt.Errorf("create scenario directory: %w", err)
	}
	if memSvc != nil {
		defer memSvc.Close()
	}
	if sessionSvc != nil {
		defer sessionSvc.Close()
	}

	log.Printf("")
	log.Printf("=== Running: %s (backend=%s) ===", evaluator.Name(), backend)

	result, evaluationErr := runEvaluation(
		ctx, samples, evaluator, config, backend, scenarioDir,
	)
	saveResults(scenarioDir, result)
	printSummary(result)
	if evaluationErr != nil {
		return fmt.Errorf(
			"evaluate %s (backend=%s): %w",
			evaluator.Name(), backend, evaluationErr,
		)
	}
	return nil
}

func buildScenarioDir(outputDir string, scenario scenarios.ScenarioType, backend string) string {
	if scenario == scenarios.ScenarioLongContext {
		return filepath.Join(outputDir, string(scenario))
	}
	return filepath.Join(outputDir, fmt.Sprintf("%s_%s", scenario, backend))
}

func validateLoCoMoFlags() {
	validScenarios := map[string]bool{
		"long_context":   true,
		"session_recall": true,
		"agentic":        true,
		"auto":           true,
		"all":            true,
	}
	for _, s := range strings.Split(*flagScenario, ",") {
		s = strings.TrimSpace(s)
		if !validScenarios[s] {
			log.Fatalf("Invalid scenario: %s", s)
		}
	}
	if *flagMaxContext <= 0 {
		log.Fatalf("Invalid max-context: %d", *flagMaxContext)
	}
	if *flagSessionEventLimit < 0 {
		log.Fatalf("Invalid session-event-limit: %d", *flagSessionEventLimit)
	}
	if *flagSessionRecallMinScore < 0 {
		log.Fatalf(
			"Invalid session-recall-min-score: %f",
			*flagSessionRecallMinScore,
		)
	}
	if *flagLoCoMoReuseMemories && strings.TrimSpace(*flagTableSuffix) == "" {
		log.Fatal("locomo-reuse-memories requires an explicit table-suffix")
	}
}

func buildMemoryConfig(
	scenarioType scenarios.ScenarioType,
	backend string,
) memoryConfig {
	switch scenarioType {
	case scenarios.ScenarioAuto:
		return memoryConfig{
			backend: backend,
			mode:    memoryModeAuto,
		}
	case scenarios.ScenarioAgentic:
		return memoryConfig{
			backend: backend,
			mode:    memoryModeManual,
		}
	default:
		return memoryConfig{
			mode: memoryModeNone,
		}
	}
}

func buildMemoryServiceOptions(
	cfg memoryConfig,
	extractorModel model.Model,
) (memoryServiceOptions, error) {
	opts := memoryServiceOptions{
		vectorTopK:       *flagVectorTopK,
		memoryJobTimeout: *flagAutoMemoryJobTimeout,
	}
	if cfg.mode != memoryModeAuto {
		return opts, nil
	}
	opts.enableExtractor = true
	opts.extractorModel = extractorModel
	if cfg.backend == "pgvector" {
		config, err := currentPGVectorExtractionConfig()
		if err != nil {
			return memoryServiceOptions{}, err
		}
		if config.AssistantResultExtraction {
			log.Printf("Warning: LoCoMo maps one human speaker per session to the assistant role; " +
				"assistant-result extraction is a synthetic-role ablation, not a real assistant-output evaluation")
		}
		opts.pgvectorExtraction = config
	}
	return opts, nil
}

type memoryServiceOptions struct {
	enableExtractor    bool
	extractorModel     model.Model
	extractorCallbacks *model.Callbacks
	embeddingTracker   *lmeTrackingEmbedder
	vectorTopK         int
	memoryJobTimeout   time.Duration
	pgvectorExtraction pgvectorExtractionConfig
}

func createMemoryService(
	cfg memoryConfig,
	opts memoryServiceOptions,
) (memory.Service, error) {
	switch cfg.backend {
	case "pgvector":
		return createPGVectorService(opts)
	case "mysql":
		return createMySQLService(opts)
	case "sqlite":
		return createSQLiteService(opts)
	case "sqlitevec":
		return createSQLiteVecService(opts)
	default:
		return createInMemoryService(opts), nil
	}
}

func createSessionRecallService(
	cfg scenarios.Config,
) (session.Service, error) {
	dsn := getPGVectorDSN()
	if dsn == "" {
		return nil, fmt.Errorf(
			"pgvector-dsn or PGVECTOR_DSN is required for session_recall scenario",
		)
	}
	embedModelName := getEmbedModelName()
	emb := newEmbeddingEmbedder(embedModelName)
	log.Printf(
		"Creating session recall pgvector service (embed_model=%s)",
		embedModelName,
	)
	return sessionpgvector.NewService(
		sessionpgvector.WithPostgresClientDSN(dsn),
		sessionpgvector.WithEmbedder(emb),
		sessionpgvector.WithIndexDimension(emb.GetDimensions()),
		sessionpgvector.WithSessionEventLimit(cfg.SessionEventLimit),
		sessionpgvector.WithMaxResults(cfg.SessionRecallResults),
		sessionpgvector.WithTablePrefix(
			tableNameWithSuffix(sessionRecallTableBase),
		),
		sessionpgvector.WithSyncIndexing(true),
	)
}

func createPGVectorService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	dsn := getPGVectorDSN()
	if dsn == "" {
		return nil, fmt.Errorf(
			"pgvector-dsn or PGVECTOR_DSN is required for pgvector backend",
		)
	}
	embedModelName := getEmbedModelName()
	var emb embedder.Embedder = newEmbeddingEmbedder(embedModelName)
	if opts.embeddingTracker != nil {
		emb = opts.embeddingTracker
	}
	tableName := tableNameWithSuffix(pgvectorTableDefaultBase)
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf(
			"Creating pgvector memory service with extractor "+
				"(embed_model=%s)",
			embedModelName,
		)
		tableName = tableNameWithSuffix(pgvectorTableAutoBase)
		extractorOptions, err := pgvectorExtractorOptions(
			opts.pgvectorExtraction,
		)
		if err != nil {
			return nil, err
		}
		ext = newMemoryExtractor(opts, extractorOptions...)
	} else {
		log.Printf(
			"Creating pgvector memory service (embed_model=%s)",
			embedModelName,
		)
	}
	svcOpts := []memorypgvector.ServiceOpt{
		memorypgvector.WithPGVectorClientDSN(dsn),
		memorypgvector.WithEmbedder(emb),
		memorypgvector.WithMaxResults(opts.vectorTopK),
		memorypgvector.WithTableName(tableName),
		memorypgvector.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(svcOpts,
			memorypgvector.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorypgvector.WithMemoryQueueSize(autoMemoryQueueSize),
			memorypgvector.WithMemoryJobTimeout(opts.memoryJobTimeout),
		)
	}
	return memorypgvector.NewService(svcOpts...)
}

func createMySQLService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	dsn := getMySQLDSN()
	if dsn == "" {
		return nil, fmt.Errorf(
			"mysql-dsn or MYSQL_DSN is required for mysql backend",
		)
	}

	tableName := tableNameWithSuffix(mysqlTableDefaultBase)
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf("Creating mysql memory service with extractor")
		tableName = tableNameWithSuffix(mysqlTableAutoBase)
		ext = newMemoryExtractor(opts)
	} else {
		log.Printf("Creating mysql memory service")
	}

	svcOpts := []memorymysql.ServiceOpt{
		memorymysql.WithMySQLClientDSN(dsn),
		memorymysql.WithTableName(tableName),
		memorymysql.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(svcOpts,
			memorymysql.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorymysql.WithMemoryQueueSize(autoMemoryQueueSize),
			memorymysql.WithMemoryJobTimeout(opts.memoryJobTimeout),
		)
	}
	return memorymysql.NewService(svcOpts...)
}

func createInMemoryService(opts memoryServiceOptions) memory.Service {
	if opts.enableExtractor {
		log.Printf("Creating inmemory memory service with extractor")
		ext := newMemoryExtractor(opts)
		return inmemory.NewMemoryService(
			inmemory.WithExtractor(ext),
			inmemory.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			inmemory.WithMemoryQueueSize(autoMemoryQueueSize),
			inmemory.WithMemoryJobTimeout(opts.memoryJobTimeout),
		)
	}
	return inmemory.NewMemoryService()
}

func newMemoryExtractor(
	opts memoryServiceOptions,
	extractorOptions ...extractor.Option,
) extractor.MemoryExtractor {
	if opts.extractorCallbacks != nil {
		extractorOptions = append(
			extractorOptions,
			extractor.WithModelCallbacks(opts.extractorCallbacks),
		)
	}
	return extractor.NewExtractor(opts.extractorModel, extractorOptions...)
}

// standardCategories is the ordered list of QA categories.
var standardCategories = []string{
	"single-hop", "multi-hop", "temporal",
	"open-domain", "adversarial",
}

func runEvaluation(
	ctx context.Context,
	samples []*dataset.LoCoMoSample,
	evaluator scenarios.Evaluator,
	config scenarios.Config,
	backend string,
	scenarioDir string,
) (*EvaluationResult, error) {
	startTime := time.Now()
	catAgg := metrics.NewCategoryAggregator()
	sampleResults := make([]*scenarios.SampleResult, 0, len(samples))
	failures := make([]EvaluationFailure, 0)
	var totalQuestions int
	var totalUsage scenarios.TokenUsage

	for i, sample := range samples {
		log.Printf("[%d/%d] Evaluating sample: %s (%d QA)",
			i+1, len(samples), sample.SampleID, len(sample.QA))

		sampleStart := time.Now()
		result, err := evaluator.Evaluate(ctx, sample)
		if err != nil {
			log.Printf("  Error: %v", err)
			failure := evaluationFailure(
				sample.SampleID, time.Since(sampleStart), result, err,
			)
			failures = append(failures, failure)
			if failure.TokenUsage != nil {
				totalUsage.Add(*failure.TokenUsage)
			}
			partial := buildEvaluationResult(
				config, backend, startTime,
				sampleResults, catAgg, totalQuestions, totalUsage,
			)
			attachEvaluationFailures(partial, failures)
			saveResults(scenarioDir, partial)
			continue
		}

		sampleResults = append(sampleResults, result)
		totalQuestions += len(result.QAResults)

		// Aggregate category metrics.
		for _, qaResult := range result.QAResults {
			catAgg.Add(qaResult.Category, qaResult.Metrics)
		}

		// Aggregate token usage.
		if result.TokenUsage != nil {
			totalUsage.Add(*result.TokenUsage)
		}

		log.Printf("  Completed in %v | F1=%.3f BLEU=%.3f",
			time.Since(sampleStart).Round(time.Millisecond),
			result.Overall.F1,
			result.Overall.BLEU)
		if result.TokenUsage != nil &&
			result.TokenUsage.LLMCalls > 0 {
			if result.TokenUsage.CachedTokens > 0 {
				log.Printf(
					"  Tokens: prompt=%d cached=%d"+
						" completion=%d calls=%d",
					result.TokenUsage.PromptTokens,
					result.TokenUsage.CachedTokens,
					result.TokenUsage.CompletionTokens,
					result.TokenUsage.LLMCalls,
				)
			} else {
				log.Printf(
					"  Tokens: prompt=%d"+
						" completion=%d calls=%d",
					result.TokenUsage.PromptTokens,
					result.TokenUsage.CompletionTokens,
					result.TokenUsage.LLMCalls,
				)
			}
		}

		// Log per-sample category breakdown.
		logSampleCategoryBreakdown(result)

		// Incremental checkpoint: save partial results after
		// each sample so progress is not lost.
		partial := buildEvaluationResult(
			config, backend, startTime,
			sampleResults, catAgg, totalQuestions, totalUsage,
		)
		attachEvaluationFailures(partial, failures)
		saveResults(scenarioDir, partial)
	}

	result := buildEvaluationResult(
		config, backend, startTime,
		sampleResults, catAgg, totalQuestions, totalUsage,
	)
	attachEvaluationFailures(result, failures)
	if len(failures) > 0 {
		return result, fmt.Errorf(
			"%d sample(s) failed; first failure %s: %s",
			len(failures), failures[0].SampleID, failures[0].Error,
		)
	}
	return result, nil
}

func evaluationFailure(
	sampleID string,
	duration time.Duration,
	result *scenarios.SampleResult,
	err error,
) EvaluationFailure {
	failure := EvaluationFailure{
		SampleID:    sampleID,
		Error:       err.Error(),
		TotalTimeMs: duration.Milliseconds(),
	}
	if result == nil {
		return failure
	}
	failure.TotalTimeMs = result.TotalTimeMs
	failure.TokenUsage = result.TokenUsage
	failure.ExtractionTokenUsage = result.ExtractionTokenUsage
	failure.QATokenUsage = result.QATokenUsage
	failure.EmbeddingUsage = result.EmbeddingUsage
	failure.ExtractionEmbeddingUsage = result.ExtractionEmbeddingUsage
	failure.QAEmbeddingUsage = result.QAEmbeddingUsage
	failure.ExtractionCalls = result.ExtractionCalls
	return failure
}

func attachEvaluationFailures(
	result *EvaluationResult,
	failures []EvaluationFailure,
) {
	if result == nil || len(failures) == 0 {
		return
	}
	result.Failures = append([]EvaluationFailure(nil), failures...)
	result.Summary.FailedSamples = len(failures)

	extractionTokens := valueOrZero(result.Summary.ExtractionTokenUsage)
	qaTokens := valueOrZero(result.Summary.QATokenUsage)
	embeddings := embeddingValueOrZero(result.Summary.EmbeddingUsage)
	extractionEmbeddings := embeddingValueOrZero(
		result.Summary.ExtractionEmbeddingUsage,
	)
	qaEmbeddings := embeddingValueOrZero(result.Summary.QAEmbeddingUsage)
	for _, failure := range failures {
		if failure.ExtractionTokenUsage != nil {
			extractionTokens.Add(*failure.ExtractionTokenUsage)
		}
		if failure.QATokenUsage != nil {
			qaTokens.Add(*failure.QATokenUsage)
		}
		if failure.EmbeddingUsage != nil {
			embeddings.Add(*failure.EmbeddingUsage)
		}
		if failure.ExtractionEmbeddingUsage != nil {
			extractionEmbeddings.Add(*failure.ExtractionEmbeddingUsage)
		}
		if failure.QAEmbeddingUsage != nil {
			qaEmbeddings.Add(*failure.QAEmbeddingUsage)
		}
	}
	result.Summary.ExtractionTokenUsage = locomoTokenUsagePointer(extractionTokens)
	result.Summary.QATokenUsage = locomoTokenUsagePointer(qaTokens)
	result.Summary.EmbeddingUsage = locomoEmbeddingUsagePointer(embeddings)
	result.Summary.ExtractionEmbeddingUsage = locomoEmbeddingUsagePointer(
		extractionEmbeddings,
	)
	result.Summary.QAEmbeddingUsage = locomoEmbeddingUsagePointer(qaEmbeddings)
}

func valueOrZero(value *scenarios.TokenUsage) scenarios.TokenUsage {
	if value == nil {
		return scenarios.TokenUsage{}
	}
	return *value
}

func embeddingValueOrZero(
	value *scenarios.EmbeddingUsage,
) scenarios.EmbeddingUsage {
	if value == nil {
		return scenarios.EmbeddingUsage{}
	}
	return *value
}

// logSampleCategoryBreakdown prints a one-line per-category
// summary for the completed sample.
func logSampleCategoryBreakdown(result *scenarios.SampleResult) {
	if len(result.ByCategory) == 0 {
		return
	}
	parts := make([]string, 0, len(standardCategories))
	for _, cat := range standardCategories {
		m, ok := result.ByCategory[cat]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s: F1=%.3f", cat, m.F1,
		))
	}
	if len(parts) > 0 {
		log.Printf("  Categories: %s", strings.Join(parts, " | "))
	}
}

// buildEvaluationResult constructs the full result from
// accumulated data.
func buildEvaluationResult(
	config scenarios.Config,
	backend string,
	startTime time.Time,
	sampleResults []*scenarios.SampleResult,
	catAgg *metrics.CategoryAggregator,
	totalQuestions int,
	totalUsage scenarios.TokenUsage,
) *EvaluationResult {
	totalTime := time.Since(startTime)
	overall := catAgg.GetOverall()
	qCount := max(totalQuestions, 1)
	protocolViolations := countProtocolViolations(sampleResults)
	recoveryAttempts, recoverySuccesses, recoveryApplied,
		recoveryRetained, recoveryFallbacks :=
		countAnswerRecoveries(sampleResults)
	phaseUsage := aggregateLoCoMoPhaseUsage(sampleResults)
	effectiveCachedTokens := totalUsage.CachedPromptTokens()
	var cacheHitRate float64
	if totalUsage.PromptTokens > 0 {
		cacheHitRate = float64(effectiveCachedTokens) /
			float64(totalUsage.PromptTokens)
	}
	metadata := &EvalMetadata{
		Framework:      "trpc-agent-go",
		Version:        "1.0.0",
		Timestamp:      time.Now(),
		Model:          getModelName(),
		ModelVariant:   getModelVariant(),
		EvalModel:      getEvalModelName(),
		Scenario:       string(config.Scenario),
		MemoryBackend:  backend,
		MaxContext:     config.MaxContext,
		QAHistoryTurns: config.QAHistoryTurns,
		QASearchPasses: config.QASearchPasses,
		ReuseMemories:  config.ReuseMemories,
		TableSuffix:    *flagTableSuffix,
		Build:          currentLongMemEvalBuildProvenance(),
		LLMJudge:       config.EnableLLMJudge,
	}
	if config.Scenario == scenarios.ScenarioAuto ||
		config.Scenario == scenarios.ScenarioAgentic {
		metadata.QAPromptVersion = scenarios.MemoryQAPromptVersion
		metadata.QASearchStrategy = scenarios.MemoryQASearchStrategy
		metadata.QARecoveryMaxTokens = scenarios.MemoryQARecoveryMaxTokens
	}
	if config.Scenario == scenarios.ScenarioAuto {
		metadata.ReplayProtocol = locomoAutoReplayProtocol
		metadata.RoleMapping = locomoRoleMapping
		metadata.AutoMemoryJobTimeout = flagAutoMemoryJobTimeout.String()
		metadata.TokenUsageScope = "extractor and QA LLM calls; " +
			"optional LLM judge excluded"
		if config.AutoExtractionTimeout > 0 {
			metadata.AutoExtractionTimeout =
				config.AutoExtractionTimeout.String()
		} else {
			metadata.AutoExtractionTimeout = "derived-from-session-count"
		}
	}
	if config.Scenario == scenarios.ScenarioAuto && backend == "pgvector" {
		metadata.VectorTopK = *flagVectorTopK
		metadata.EmbeddingUsageScope = "provider-reported extraction, " +
			"persistence, and QA-search embedding calls"
		if extraction, err := currentPGVectorExtractionConfig(); err == nil {
			metadata.PGVectorExtraction = &extraction
		}
	}
	return &EvaluationResult{
		Metadata: metadata,
		Summary: &EvalSummary{
			TotalSamples:             len(sampleResults),
			TotalQuestions:           totalQuestions,
			OverallF1:                overall.F1,
			OverallBLEU:              overall.BLEU,
			OverallLLMScore:          overall.LLMScore,
			TotalTimeMs:              totalTime.Milliseconds(),
			AvgLatencyMs:             float64(totalTime.Milliseconds()) / float64(qCount),
			TotalPromptTokens:        totalUsage.PromptTokens,
			TotalCompletionTokens:    totalUsage.CompletionTokens,
			TotalTokens:              totalUsage.TotalTokens,
			TotalCachedTokens:        effectiveCachedTokens,
			TotalLLMCalls:            totalUsage.LLMCalls,
			ProtocolViolations:       protocolViolations,
			AnswerRecoveryAttempts:   recoveryAttempts,
			AnswerRecoverySuccesses:  recoverySuccesses,
			AnswerRecoveryApplied:    recoveryApplied,
			AnswerRecoveryRetained:   recoveryRetained,
			AnswerRecoveryFallbacks:  recoveryFallbacks,
			AvgPromptTokensPerQA:     float64(totalUsage.PromptTokens) / float64(qCount),
			AvgCompletionPerQA:       float64(totalUsage.CompletionTokens) / float64(qCount),
			AvgCachedTokensPerQA:     float64(effectiveCachedTokens) / float64(qCount),
			AvgLLMCallsPerQA:         float64(totalUsage.LLMCalls) / float64(qCount),
			CacheHitRate:             cacheHitRate,
			TokenUsage:               locomoTokenUsagePointer(totalUsage),
			ExtractionTokenUsage:     locomoTokenUsagePointer(phaseUsage.extractionTokens),
			QATokenUsage:             locomoTokenUsagePointer(phaseUsage.qaTokens),
			EmbeddingUsage:           locomoEmbeddingUsagePointer(phaseUsage.embeddings),
			ExtractionEmbeddingUsage: locomoEmbeddingUsagePointer(phaseUsage.extractionEmbeddings),
			QAEmbeddingUsage:         locomoEmbeddingUsagePointer(phaseUsage.qaEmbeddings),
		},
		ByCategory:    catAgg.GetCategoryMetrics(),
		SampleResults: sampleResults,
	}
}

func locomoTokenUsagePointer(
	usage scenarios.TokenUsage,
) *scenarios.TokenUsage {
	if usage.IsZero() {
		return nil
	}
	return &usage
}

func locomoEmbeddingUsagePointer(
	usage scenarios.EmbeddingUsage,
) *scenarios.EmbeddingUsage {
	if usage.IsZero() {
		return nil
	}
	return &usage
}

type locomoPhaseUsage struct {
	extractionTokens     scenarios.TokenUsage
	qaTokens             scenarios.TokenUsage
	embeddings           scenarios.EmbeddingUsage
	extractionEmbeddings scenarios.EmbeddingUsage
	qaEmbeddings         scenarios.EmbeddingUsage
}

func aggregateLoCoMoPhaseUsage(
	results []*scenarios.SampleResult,
) locomoPhaseUsage {
	var usage locomoPhaseUsage
	for _, sample := range results {
		if sample == nil {
			continue
		}
		if sample.ExtractionTokenUsage != nil {
			usage.extractionTokens.Add(*sample.ExtractionTokenUsage)
		}
		if sample.QATokenUsage != nil {
			usage.qaTokens.Add(*sample.QATokenUsage)
		}
		if sample.EmbeddingUsage != nil {
			usage.embeddings.Add(*sample.EmbeddingUsage)
		}
		if sample.ExtractionEmbeddingUsage != nil {
			usage.extractionEmbeddings.Add(
				*sample.ExtractionEmbeddingUsage,
			)
		}
		if sample.QAEmbeddingUsage != nil {
			usage.qaEmbeddings.Add(*sample.QAEmbeddingUsage)
		}
	}
	return usage
}

func countProtocolViolations(results []*scenarios.SampleResult) int {
	var count int
	for _, sample := range results {
		if sample == nil {
			continue
		}
		for _, qa := range sample.QAResults {
			if qa != nil && qa.ProtocolError != "" {
				count++
			}
		}
	}
	return count
}

func countAnswerRecoveries(
	results []*scenarios.SampleResult,
) (attempts, successes, applied, retained, fallbacks int) {
	for _, sample := range results {
		if sample == nil {
			continue
		}
		for _, qa := range sample.QAResults {
			if qa == nil || qa.AnswerRecovery == nil {
				continue
			}
			attempts++
			if qa.AnswerRecovery.Succeeded {
				successes++
			}
			if qa.AnswerRecovery.Applied {
				applied++
			}
			if qa.AnswerRecovery.InitialAnswerRetained {
				retained++
			}
			if qa.AnswerRecovery.FallbackApplied {
				fallbacks++
			}
		}
	}
	return attempts, successes, applied, retained, fallbacks
}

func saveResults(outputDir string, result *EvaluationResult) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Printf("Failed to create output directory: %v", err)
		return
	}

	// Save full results.
	resultsPath := filepath.Join(outputDir, "results.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal results: %v", err)
		return
	}
	if err := os.WriteFile(resultsPath, data, 0644); err != nil {
		log.Printf("Failed to write results: %v", err)
		return
	}
	log.Printf("Results saved to: %s", resultsPath)

	// Save checkpoint (same as results for now).
	checkpointPath := filepath.Join(outputDir, "checkpoint.json")
	if err := os.WriteFile(checkpointPath, data, 0644); err != nil {
		log.Printf("Failed to write checkpoint: %v", err)
	}
}

func printSummary(result *EvaluationResult) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Memory Evaluation Results - %s\n", result.Metadata.Scenario)
	fmt.Println(strings.Repeat("=", 60))

	fmt.Printf("\nModel: %s\n", result.Metadata.Model)
	fmt.Printf("Scenario: %s\n", result.Metadata.Scenario)
	if result.Metadata.MemoryBackend != "" {
		fmt.Printf("Memory Backend: %s\n",
			result.Metadata.MemoryBackend)
	}
	if result.Metadata.QAHistoryTurns > 0 {
		fmt.Printf("QA History Turns: %d\n",
			result.Metadata.QAHistoryTurns)
	}
	if result.Metadata.QASearchPasses > 1 {
		fmt.Printf("QA Search Passes: %d\n",
			result.Metadata.QASearchPasses)
	}
	fmt.Printf("Samples: %d | Questions: %d\n",
		result.Summary.TotalSamples, result.Summary.TotalQuestions)
	if result.Summary.FailedSamples > 0 {
		fmt.Printf("Failed Samples: %d\n", result.Summary.FailedSamples)
	}

	fmt.Println("\n--- Overall Metrics ---")
	fmt.Printf("F1 Score:   %.4f (%.1f)\n", result.Summary.OverallF1, result.Summary.OverallF1*100)
	fmt.Printf("BLEU Score: %.4f\n", result.Summary.OverallBLEU)
	if result.Summary.OverallLLMScore > 0 {
		fmt.Printf("LLM Score:  %.4f\n", result.Summary.OverallLLMScore)
	}
	fmt.Printf("Total Time: %dms | Avg Latency: %.1fms\n",
		result.Summary.TotalTimeMs, result.Summary.AvgLatencyMs)

	if result.Summary.TotalLLMCalls > 0 {
		fmt.Println("\n--- Token Usage ---")
		fmt.Printf("Prompt Tokens:     %d (avg %.0f/QA)\n",
			result.Summary.TotalPromptTokens,
			result.Summary.AvgPromptTokensPerQA)
		fmt.Printf("Completion Tokens: %d (avg %.0f/QA)\n",
			result.Summary.TotalCompletionTokens,
			result.Summary.AvgCompletionPerQA)
		fmt.Printf("Total Tokens:      %d\n",
			result.Summary.TotalTokens)
		fmt.Printf("LLM Calls:         %d (avg %.1f/QA)\n",
			result.Summary.TotalLLMCalls,
			result.Summary.AvgLLMCallsPerQA)
	}

	fmt.Println("\n--- By Category ---")
	fmt.Printf("%-15s %8s %8s %8s %8s\n", "Category", "Count", "F1", "BLEU", "LLM")
	fmt.Println(strings.Repeat("-", 51))

	categories := []string{"single-hop", "multi-hop", "temporal", "open-domain", "adversarial"}
	for _, cat := range categories {
		if m, ok := result.ByCategory[cat]; ok {
			llmStr := "-"
			if m.LLMScore > 0 {
				llmStr = fmt.Sprintf("%.3f", m.LLMScore)
			}
			fmt.Printf("%-15s %8d %8.3f %8.3f %8s\n",
				cat, m.Count, m.F1, m.BLEU, llmStr)
		}
	}

	// Print any other categories not in the standard list.
	for cat, m := range result.ByCategory {
		found := false
		for _, c := range categories {
			if c == cat {
				found = true
				break
			}
		}
		if !found {
			llmStr := "-"
			if m.LLMScore > 0 {
				llmStr = fmt.Sprintf("%.3f", m.LLMScore)
			}
			fmt.Printf("%-15s %8d %8.3f %8.3f %8s\n",
				cat, m.Count, m.F1, m.BLEU, llmStr)
		}
	}

	fmt.Println(strings.Repeat("=", 60))
}
