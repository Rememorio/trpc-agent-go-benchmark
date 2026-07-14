//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mode selects how the toolsearch plugin is wired for a benchmark run.
//
// The refactored plugin no longer has an "LLM picks top-K" mode: the model
// itself calls the tool_search function against a namespace catalog. What varies
// is the query-resolution backend and how loaded tools are exposed:
//
//   - none      — no plugin; every tool is handed to the agent directly (baseline).
//   - keyword   — NewPlugin + WithToolboxes; tool_search resolves "queries" with the
//     built-in keyword matcher (the new default).
//   - embedding — as keyword, plus WithSemanticToolIndex so "queries" are ranked by
//     embedding (vector) similarity instead of keyword overlap.
//   - dispatch — as keyword, plus WithInvocationMode(DispatchToolCalls) so the
//     model sees exactly two tools (tool_search + call_tool) regardless of how many are loaded.
type Mode string

const (
	ModeNone            Mode = "none"
	ModeKeywordSearch   Mode = "keyword"
	ModeEmbeddingSearch Mode = "embedding"
	ModeDispatch        Mode = "dispatch"
)

func ParseMode(s string) (Mode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", string(ModeKeywordSearch):
		return ModeKeywordSearch, nil
	case string(ModeNone):
		return ModeNone, nil
	case string(ModeEmbeddingSearch):
		return ModeEmbeddingSearch, nil
	case string(ModeDispatch):
		return ModeDispatch, nil
	default:
		return "", fmt.Errorf("invalid mode: %q (valid: none|keyword|embedding|dispatch)", s)
	}
}

type BenchmarkConfig struct {
	AppName   string
	EvalSetID string
	DataDir   string
	OutputDir string

	NumRuns    int
	ModelName  string
	Mode       Mode
	MaxTools   int
	EmbedModel string
}

func (c BenchmarkConfig) Validate() error {
	if strings.TrimSpace(c.AppName) == "" {
		return fmt.Errorf("app name is empty")
	}
	if strings.TrimSpace(c.EvalSetID) == "" {
		return fmt.Errorf("evalset id is empty")
	}
	if c.NumRuns < 1 {
		return fmt.Errorf("num runs must be >= 1")
	}
	if strings.TrimSpace(c.ModelName) == "" {
		return fmt.Errorf("model name is empty")
	}
	if c.MaxTools < 1 {
		return fmt.Errorf("max tools must be >= 1")
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return fmt.Errorf("data dir is empty")
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return fmt.Errorf("output dir is empty")
	}
	if _, err := os.Stat(c.DataDir); err != nil {
		return fmt.Errorf("data dir not found: %w", err)
	}

	// Evaluation reads user inputs from <dataDir>/<app>/<evalset>.evalset.json.
	evalsetPath := filepath.Join(c.DataDir, c.AppName, c.EvalSetID+".evalset.json")
	if _, err := os.Stat(evalsetPath); err != nil {
		return fmt.Errorf("evalset file not found: %s: %w", evalsetPath, err)
	}
	metricsPath := filepath.Join(c.DataDir, c.AppName, c.EvalSetID+".metrics.json")
	if _, err := os.Stat(metricsPath); err != nil {
		return fmt.Errorf("metrics file not found: %s: %w", metricsPath, err)
	}

	_ = filepath.Clean(c.DataDir)
	_ = filepath.Clean(c.OutputDir)
	return nil
}
