//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main provides memory evaluation benchmarks for trpc-agent-go.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// Command-line flags.
var (
	flagModel        = flag.String("model", "", "Model name (env MODEL_NAME or gpt-4o-mini)")
	flagModelVariant = flag.String(
		"model-variant",
		"",
		"OpenAI-compatible model variant: openai, deepseek, hunyuan, qwen, glm (env MODEL_VARIANT)",
	)
	flagEvalModel     = flag.String("eval-model", "", "Evaluation model for LLM judge")
	flagDataset       = flag.String("dataset", defaultLoCoMoDataset, "Dataset directory or JSON file")
	flagDataFile      = flag.String("data-file", defaultLoCoMoDataFile, "Dataset file name")
	flagOutput        = flag.String("output", "../results", "Output directory")
	flagDatasetFormat = flag.String(
		"dataset-format",
		"locomo",
		"Dataset format: locomo or longmemeval",
	)

	flagScenario = flag.String(
		"scenario",
		"long_context",
		"Evaluation scenario (comma-separated): "+
			"long_context, session_recall, agentic, auto, all",
	)
	flagMemoryBackends = flag.String(
		"memory-backend",
		defaultMemoryBackend,
		"Memory backends (comma-separated): "+
			"inmemory, sqlite, sqlitevec, pgvector, mysql, mem0 (longmemeval only)",
	)
	flagPGVectorDSN = flag.String(
		"pgvector-dsn",
		"",
		"PostgreSQL DSN for pgvector (env PGVECTOR_DSN)",
	)
	flagEmbedModel = flag.String(
		"embed-model",
		"",
		"Embedding model for vector backends (pgvector, sqlitevec) "+
			"(env EMBED_MODEL_NAME or text-embedding-3-small)",
	)
	flagVectorTopK = flag.Int(
		"vector-topk",
		30,
		"Top-k results for vector backends (pgvector, sqlitevec)",
	)
	flagSessionRecallMinScore = flag.Float64(
		"session-recall-min-score",
		0.3,
		"Minimum score for preloaded session recall hits",
	)
	flagMySQLDSN = flag.String(
		"mysql-dsn",
		"",
		"MySQL DSN for mysql backend (env MYSQL_DSN)",
	)

	flagSampleID          = flag.String("sample-id", "", "Filter by sample ID")
	flagCategory          = flag.String("category", "", "Filter by QA category")
	flagLoCoMoQuestionIDs = flag.String(
		"locomo-question-ids",
		"",
		"Comma-separated LoCoMo question_id filter",
	)
	flagMaxTasks          = flag.Int("max-tasks", 0, "Maximum tasks (0=all)")
	flagMaxContext        = flag.Int("max-context", 128000, "Maximum context length")
	flagSessionEventLimit = flag.Int("session-event-limit", 1000, "Max events kept in each session (0=unlimited)")
	flagQAHistoryTurns    = flag.Int(
		"qa-history-turns", 0,
		"Recent conversation turns injected as context during QA (0=none, auto/agentic only)",
	)
	flagQASearchPasses = flag.Int(
		"qa-search-passes",
		2,
		"Number of memory_search calls per QA "+
			"(1=single search, auto/agentic only)",
	)
	flagAutoExtractionTimeout = flag.Duration(
		"auto-extraction-timeout",
		0,
		"Total wait timeout for LoCoMo auto extraction (0=derive from session count)",
	)
	flagAutoMemoryJobTimeout = flag.Duration(
		"auto-memory-job-timeout",
		defaultAutoMemoryJobTimeout,
		"Timeout for each LoCoMo auto-memory extraction job",
	)
	flagLLMJudge = flag.Bool("llm-judge", false, "Enable LLM-as-Judge evaluation")
	flagVerbose  = flag.Bool("verbose", false, "Verbose output")

	flagDebugDumpMemories = flag.Bool("debug-dump-memories", false, "Dump extracted memories (auto scenario only)")
	flagDebugMemLimit     = flag.Int("debug-mem-limit", 200, "Max memories to dump when debug-dump-memories is enabled")
	flagDebugQALimit      = flag.Int("debug-qa-limit", 5, "Dump retrieval hits for the first N questions (auto scenario only)")
	flagResume            = flag.Bool("resume", false, "Resume from checkpoint (TODO: implement)")
	flagTableSuffix       = flag.String(
		"table-suffix",
		"",
		"Suffix appended to all DB table names for parallel runs "+
			"(e.g. _v2 -> memory_eval_auto_v2)",
	)

	flagLMEQuestionID = flag.String(
		"lme-question-id",
		"",
		"Only run the given LongMemEval question_id",
	)
	flagLMEQuestionIDs = flag.String(
		"lme-question-ids",
		"",
		"Comma-separated LongMemEval question_id filter",
	)
	flagLMEExcludeQuestionIDs = flag.String(
		"lme-exclude-question-ids",
		"",
		"Comma-separated LongMemEval question_ids excluded before sampling",
	)
	flagLMEExcludeQuestionIDsFile = flag.String(
		"lme-exclude-question-ids-file",
		"",
		"File containing one LongMemEval question_id per line to exclude before sampling",
	)
	flagLMEQuestionTypes = flag.String(
		"lme-question-types",
		"",
		"Comma-separated LongMemEval question_type filter",
	)
	flagLMEPerType = flag.Int(
		"lme-per-type",
		0,
		"Stratified LongMemEval sample count per question_type (0=disabled)",
	)
	flagLMEAbstentionCount = flag.Int(
		"lme-abstention-count",
		0,
		"Additional LongMemEval abstention questions to sample by question_id suffix (0=none)",
	)
	flagLMESampleSeed = flag.Int64(
		"lme-sample-seed",
		42,
		"Random seed for LongMemEval stratified sampling",
	)
	flagLMESelectionOnly = flag.Bool(
		"lme-selection-only",
		false,
		"Print or validate the selected LongMemEval question IDs and provenance, then exit before provider setup",
	)
	flagLMEPreregisteredSelection = flag.String(
		"lme-preregistered-selection",
		"",
		"Execute the exact LongMemEval selection from a preregistration manifest",
	)
	flagLMEMaxSessions = flag.Int(
		"lme-max-sessions",
		0,
		"Max LongMemEval haystack sessions to ingest per case (0=all)",
	)
	flagLMEMaxPairs = flag.Int(
		"lme-max-pairs",
		0,
		"Max LongMemEval user/assistant pairs to ingest per case (0=all)",
	)
	flagLMEUserScope = flag.String(
		"lme-user-scope",
		"",
		"Stable memory user-ID scope for paired LongMemEval runs (empty=unique per run)",
	)
	flagLMEIngestWait = flag.Duration(
		"lme-ingest-wait",
		250*time.Millisecond,
		"Wait after each LongMemEval pair ingestion before diffing memories",
	)
	flagPGVectorUpdatePolicy = flag.String(
		"pgvector-update-policy",
		"reconcile",
		"PGVector auto-memory update policy: reconcile or add-only",
	)
	flagPGVectorAssistantResultExtraction = flag.Bool(
		"pgvector-assistant-result-extraction",
		false,
		"Extract concrete assistant-produced results into PGVector auto memory",
	)
	flagLMEModelCallTimeout = flag.Duration(
		"lme-model-call-timeout",
		5*time.Minute,
		"Timeout for each LongMemEval model call (0=disabled)",
	)
	flagLMEAnswer = flag.Bool(
		"lme-answer",
		true,
		"Generate LongMemEval answers from retrieved memories",
	)
	flagLMEAnswerTopK = flag.Int(
		"lme-answer-top-k",
		0,
		"Maximum ranked memories passed to the LongMemEval answer model "+
			"(0=all retrieved memories)",
	)
	flagLMEBlindProgress = flag.Bool(
		"lme-blind-progress",
		false,
		"Redact LongMemEval identifiers and outcome content from progress and per-case logs",
	)
	flagLMEImplementation = flag.String(
		"lme-implementation",
		"",
		"Implementation label recorded in LongMemEval results; defaults to pinned build identity (env LME_IMPLEMENTATION)",
	)
	flagLMEAnalyzeResults = flag.String(
		"lme-analyze-results",
		"",
		"Analyze an existing LongMemEval results.json and write analysis files",
	)
	flagLMEHydrateLogicalUsageResults = flag.String(
		"lme-hydrate-logical-usage-results",
		"",
		"Backfill cached extraction token usage in an existing LongMemEval results.json",
	)
	flagLMEReanswerResults = flag.String(
		"lme-reanswer-results",
		"",
		"Regenerate answers from retrieval hits in an existing LongMemEval results.json",
	)
	flagLMEReanswerReuseSourceAnswers = flag.Bool(
		"lme-reanswer-reuse-source-answers",
		true,
		"Seed the answer cache from compatible answers in the source LongMemEval results",
	)
	flagLMERefreshRetrievalResults = flag.String(
		"lme-refresh-retrieval-results",
		"",
		"Re-run PGVector retrieval and answers against memories persisted by an existing LongMemEval results.json",
	)
	flagLMEAllowSharedTableRefresh = flag.Bool(
		"lme-allow-shared-table-refresh",
		false,
		"Allow retrieval refresh from a legacy LongMemEval run without a table suffix after validating its explicit user scope",
	)
	flagLMERefreshMemorySnapshots = flag.String(
		"lme-refresh-memory-snapshots",
		"",
		"Refresh final memory snapshots and evidence from persisted LongMemEval memories without model calls",
	)
	flagLMERerankResults = flag.String(
		"lme-rerank-results",
		"",
		"Rerank every backend's saved retrieval hits and regenerate answers",
	)
	flagLMERerankTopN = flag.Int(
		"lme-rerank-topn",
		12,
		"Maximum directly relevant memories selected by the saved-result reranker",
	)
	flagLMEJudgeResults = flag.String(
		"lme-judge-results",
		"",
		"Run an LLM semantic judge over an existing LongMemEval results.json",
	)
	flagLMEJudgeRuns = flag.Int(
		"lme-judge-runs",
		1,
		"Odd number of independent LongMemEval judge votes",
	)
	flagLMEJudgeCache = flag.String(
		"lme-judge-cache",
		"",
		"Shared content-addressed LongMemEval judge cache file",
	)
	flagLMEAnswerCache = flag.String(
		"lme-answer-cache",
		"",
		"Shared content-addressed LongMemEval answer cache file",
	)
	flagLMEModelResponseCache = flag.String(
		"lme-model-response-cache",
		"",
		"Persistent content-addressed model response cache for primary LongMemEval runs",
	)
	flagLMEModelResponseCacheRequireHit = flag.Bool(
		"lme-model-response-cache-require-hit",
		false,
		"Fail before a model provider call when a LongMemEval cache entry is missing",
	)
	flagLMEEmbeddingResponseCache = flag.String(
		"lme-embedding-response-cache",
		"",
		"Persistent content-addressed embedding response cache for LongMemEval runs",
	)
	flagLMEEmbeddingResponseCacheRequireHit = flag.Bool(
		"lme-embedding-response-cache-require-hit",
		false,
		"Fail before an embedding provider call when a LongMemEval cache entry is missing",
	)
	flagLoCoMoReuseMemories = flag.Bool(
		"locomo-reuse-memories",
		false,
		"Reuse memories already stored in the selected backend table and run only LoCoMo QA",
	)
	flagLMECompareResults = flag.String(
		"lme-compare-results",
		"",
		"Compare two LongMemEval results.json files (baseline,candidate)",
	)
	flagLMECompareReplicates = flag.String(
		"lme-compare-replicates",
		"",
		"Aggregate and gate LongMemEval answer replicates from a comparison manifest",
	)
	flagMem0Host = flag.String(
		"mem0-host",
		"",
		"Mem0 OSS host (env MEM0_HOST or http://localhost:8888)",
	)
	flagMem0Implementation = flag.String(
		"mem0-implementation",
		"",
		"Mem0 source revision or image digest recorded in LongMemEval results "+
			"(env MEM0_IMPLEMENTATION)",
	)
	flagMem0Cloud = flag.Bool(
		"mem0-cloud",
		false,
		"Use hosted mem0 API semantics instead of self-hosted OSS",
	)
	flagMem0LLMTemperature = flag.Float64(
		"mem0-llm-temperature",
		-1,
		"Configure self-hosted mem0 LLM temperature before a run (-1=leave unchanged)",
	)
)

const (
	datasetFormatLoCoMo      = "locomo"
	datasetFormatLongMemEval = "longmemeval"

	defaultLoCoMoDataset  = "../data"
	defaultLoCoMoDataFile = "locomo10.json"
	defaultMemoryBackend  = "inmemory"

	maxQASearchPasses = 3
)

func main() {
	flag.Parse()
	validateFlags()

	ctx := context.Background()
	if isLongMemEvalDatasetFormat() {
		if err := runLongMemEvalMemory(ctx); err != nil {
			log.Fatalf("LongMemEval memory evaluation failed: %v", err)
		}
		return
	}

	if err := runLoCoMoMemory(ctx); err != nil {
		log.Fatalf("LoCoMo memory evaluation failed: %v", err)
	}
}

// tableNameWithSuffix appends the user-specified suffix to a base table name.
func tableNameWithSuffix(base string) string {
	if *flagTableSuffix == "" {
		return base
	}
	return base + *flagTableSuffix
}

func validateFlags() {
	switch strings.ToLower(strings.TrimSpace(*flagDatasetFormat)) {
	case datasetFormatLoCoMo, datasetFormatLongMemEval:
	default:
		log.Fatalf("Invalid dataset-format: %s", *flagDatasetFormat)
	}
	if *flagVectorTopK < 1 {
		log.Fatalf("Invalid vector-topk: %d", *flagVectorTopK)
	}
	if *flagLMEAnswerTopK < 0 ||
		(*flagLMEAnswerTopK > 0 && *flagLMEAnswerTopK > *flagVectorTopK) {
		log.Fatalf(
			"Invalid lme-answer-top-k: %d (expected 0 or 1..%d)",
			*flagLMEAnswerTopK,
			*flagVectorTopK,
		)
	}
	if *flagQASearchPasses < 1 ||
		*flagQASearchPasses > maxQASearchPasses {
		log.Fatalf(
			"Invalid qa-search-passes: %d (range: 1-%d)",
			*flagQASearchPasses,
			maxQASearchPasses,
		)
	}
	if *flagAutoExtractionTimeout < 0 {
		log.Fatalf(
			"Invalid auto-extraction-timeout: %s",
			*flagAutoExtractionTimeout,
		)
	}
	if *flagAutoMemoryJobTimeout <= 0 {
		log.Fatalf(
			"Invalid auto-memory-job-timeout: %s",
			*flagAutoMemoryJobTimeout,
		)
	}

	validBackends := map[string]bool{
		"inmemory":  true,
		"sqlite":    true,
		"sqlitevec": true,
		"pgvector":  true,
		"mysql":     true,
	}
	if isLongMemEvalDatasetFormat() {
		if strings.TrimSpace(*flagMemoryBackends) == defaultMemoryBackend {
			*flagMemoryBackends = "pgvector,mem0"
		}
		validBackends = map[string]bool{
			"pgvector": true,
			"mem0":     true,
		}
	} else {
		validateLoCoMoFlags()
	}
	backends := parseMemoryBackends(*flagMemoryBackends)
	for _, b := range backends {
		if !validBackends[b] {
			log.Fatalf("Invalid memory backend: %s", b)
		}
	}
	if err := validatePGVectorExtractionFlags(backends); err != nil {
		log.Fatalf("Invalid pgvector extraction configuration: %v", err)
	}

	if *flagMaxTasks < 0 {
		log.Fatalf("Invalid max-tasks: %d", *flagMaxTasks)
	}
	if *flagLMEMaxSessions < 0 {
		log.Fatalf("Invalid lme-max-sessions: %d", *flagLMEMaxSessions)
	}
	if *flagLMEMaxPairs < 0 {
		log.Fatalf("Invalid lme-max-pairs: %d", *flagLMEMaxPairs)
	}
	if *flagLMEPerType < 0 {
		log.Fatalf("Invalid lme-per-type: %d", *flagLMEPerType)
	}
	if *flagLMEAbstentionCount < 0 {
		log.Fatalf("Invalid lme-abstention-count: %d", *flagLMEAbstentionCount)
	}
}

func isLongMemEvalDatasetFormat() bool {
	return strings.EqualFold(strings.TrimSpace(*flagDatasetFormat), datasetFormatLongMemEval)
}

func parseMemoryBackends(backendsStr string) []string {
	parts := strings.Split(backendsStr, ",")
	backends := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			backends = append(backends, p)
		}
	}
	return backends
}

func parseCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func getModelName() string {
	if *flagModel != "" {
		return *flagModel
	}
	if env := os.Getenv("MODEL_NAME"); env != "" {
		return env
	}
	return "gpt-4o-mini"
}

func getModelVariant() string {
	if *flagModelVariant != "" {
		return *flagModelVariant
	}
	return os.Getenv("MODEL_VARIANT")
}

func getEvalModelName() string {
	if *flagEvalModel != "" {
		return *flagEvalModel
	}
	if env := os.Getenv("EVAL_MODEL_NAME"); env != "" {
		return env
	}
	return getModelName()
}

func getEmbedModelName() string {
	if *flagEmbedModel != "" {
		return *flagEmbedModel
	}
	if env := os.Getenv("EMBED_MODEL_NAME"); env != "" {
		return env
	}
	return "text-embedding-3-small"
}

const (
	envOpenAIBaseURL          = "OPENAI_BASE_URL"
	envOpenAIEmbeddingAPIKey  = "OPENAI_EMBEDDING_API_KEY"
	envOpenAIEmbeddingBaseURL = "OPENAI_EMBEDDING_BASE_URL"
)

var lmeEmbeddingRetryBackoff = []time.Duration{
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	60 * time.Second,
	90 * time.Second,
}

func newEmbeddingEmbedder(modelName string) *openai.Embedder {
	opts := []openai.Option{
		openai.WithModel(modelName),
		openai.WithMaxRetries(len(lmeEmbeddingRetryBackoff)),
		openai.WithRetryBackoff(lmeEmbeddingRetryBackoff),
	}

	if apiKey := os.Getenv(envOpenAIEmbeddingAPIKey); apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}

	baseURL := os.Getenv(envOpenAIEmbeddingBaseURL)
	if baseURL == "" {
		baseURL = os.Getenv(envOpenAIBaseURL)
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	return openai.New(opts...)
}

func getPGVectorDSN() string {
	if *flagPGVectorDSN != "" {
		return *flagPGVectorDSN
	}
	return os.Getenv("PGVECTOR_DSN")
}

func getMySQLDSN() string {
	if *flagMySQLDSN != "" {
		return *flagMySQLDSN
	}
	return os.Getenv("MYSQL_DSN")
}
