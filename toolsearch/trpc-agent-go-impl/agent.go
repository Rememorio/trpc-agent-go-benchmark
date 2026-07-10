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

	"trpc.group/trpc-go/trpc-agent-go-benchmark/toolsearch/trpc-agent-go-impl/toolboxs"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

// baselineInstruction is used by the `none` mode: every tool is advertised
// directly, so there is no catalog to browse and no tool_search step.
const baselineInstruction = "You MUST use the provided tools to answer the user's request. " +
	"Choose the single most appropriate tool, call it exactly once, then answer using its result. " +
	"Do NOT call additional tools after you have the result, and never ask clarifying questions. " +
	"Treat the tool's returned result as authoritative and final."

// searchInstruction is used by the tool_search modes (keyword/knowledge/dispatch).
// It carries the {deferred_tools_section} placeholder the plugin replaces with
// the toolbox catalog on every model turn, plus the tool-use policy from the
// plugin's accuracy test: always call tool_search first, prefer an exact tool
// name when the catalog shows one, otherwise pick the matching namespace and
// issue bilingual keyword queries.
const searchInstruction = `You are a helpful AI assistant.

Tool-use policy (MANDATORY, applies to every user turn):
1. NEVER ask the user clarifying questions. Whatever the user asks you to do,
   immediately call the tool_search function to load the matching tool(s).
   Do NOT reply with plain prose saying "I cannot do that" — always call
   tool_search first without hesitation.
2. If a tool name in the catalog obviously matches the user's intent, pass it
   directly via tool_names.
3. Otherwise, pick the namespace from the catalog whose description best matches
   the user's intent and issue keyword queries in BOTH Chinese and English.
4. Only after the tool is loaded, call it. Never fabricate tool output.
5. Call exactly ONE deferred tool per request. Once it returns, treat its result
   as authoritative and answer immediately — do NOT call more tools or search again.

{deferred_tools_section}
`

// NewInstrumentedRunner builds the benchmark runner for the configured mode.
// It wires an embedder that folds each turn's tool_search embedding usage
// (knowledge mode only) into the collector, then wraps the runner in a
// CountingRunner that records per-turn chat usage and wall time.
func NewInstrumentedRunner(cfg BenchmarkConfig, collector *Collector) (runner.Runner, error) {
	chatModel := openai.New(cfg.ModelName)

	agentTools, instruction := agentToolsAndInstruction(cfg)

	ag := llmagent.New(
		"toolsearch-benchmark-agent",
		llmagent.WithModel(chatModel),
		llmagent.WithTools(agentTools),
		llmagent.WithInstruction(instruction),
		llmagent.WithDescription("Benchmark agent for tool search evaluation"),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}),
	)

	plugins, err := buildPlugins(cfg, collector, chatModel)
	if err != nil {
		return nil, err
	}

	sess := inmemory.NewSessionService()
	base := runner.NewRunner(cfg.AppName, ag,
		runner.WithSessionService(sess),
		runner.WithPlugins(plugins...),
	)
	return NewCountingRunner(base, collector), nil
}

// agentToolsAndInstruction returns the tools advertised on the agent itself and
// the system instruction for the mode. In `none` mode the agent carries the
// entire catalog and uses the plain baseline instruction. In the search modes
// the agent carries only the preset tools (the plugin injects tool_search and
// the loaded deferred tools), and the instruction carries the catalog placeholder.
func agentToolsAndInstruction(cfg BenchmarkConfig) ([]tool.Tool, string) {
	if cfg.Mode == ModeNone {
		return toolboxs.AllTools(), baselineInstruction
	}
	return toolboxs.PresetTools(), searchInstruction
}

// buildPlugins wires the toolsearch plugin for the configured mode. `none`
// registers no plugin; the search modes register a single toolsearch.Plugin over
// the namespace catalog, differing only in the query backend / tool exposure.
func buildPlugins(cfg BenchmarkConfig, collector *Collector, chatModel model.Model) ([]plugin.Plugin, error) {
	if cfg.Mode == ModeNone {
		return nil, nil
	}

	opts := []toolsearch.Option{
		toolsearch.WithToolboxes(toolboxs.Toolboxes()),
		toolsearch.WithDeferredTools(toolboxs.DefaultTools()),
		toolsearch.WithMaxTools(cfg.MaxTools),
		toolsearch.WithCatalogInDescription(false),
	}

	switch cfg.Mode {
	case ModeKeywordSearch:
		// Built-in keyword matching (plugin default) — no extra options.
	case ModeDispatch:
		// Collapse the loaded toolset behind tool_search + call_tool.
		opts = append(opts, toolsearch.WithInvocationMode(toolsearch.DispatchToolCalls))
	case ModeKnowledgeSearch:
		// Wrap the embedder so its token usage lands in the tool-search bucket.
		emb := newCountingEmbedder(
			openaiembedder.New(openaiembedder.WithModel(cfg.EmbedModel)),
			collector,
		)

		toolKnowledge, err := toolsearch.NewToolKnowledge(
			emb,
			toolsearch.WithVectorStore(vectorinmemory.New()),
		)
		if err != nil {
			return nil, fmt.Errorf("create tool knowledge: %w", err)
		}
		// WithFailOpen keeps tools reachable if embedding fails mid-run.
		opts = append(opts,
			toolsearch.WithToolKnowledge(toolKnowledge),
			toolsearch.WithEmbeddingFailOpen(),
		)
	default:
		return nil, fmt.Errorf("unknown mode: %s", cfg.Mode)
	}

	tp := toolsearch.New(toolboxs.PresetTools(), opts...)
	return []plugin.Plugin{tp}, nil
}
