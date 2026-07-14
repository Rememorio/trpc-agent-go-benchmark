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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/tooltrajectory"
)

// compareNameUnwrapCallTool is the registered tool-trajectory compare used by
// the benchmark's metrics file. It normalizes the actual trajectory before
// applying the standard subset + regex + unordered match.
const compareNameUnwrapCallTool = "unwrap_call_tool"

// unwrapCallToolCompare returns a tool-trajectory CompareFunc that makes the
// metric mode-agnostic.
//
// Why it exists: in call_tool mode (WithEnableCallTool) the model never calls a
// deferred tool by name — it calls the wrapper `call_tool` with the real tool
// name in arguments.tool_name. The tool-trajectory metric matches on tool NAME,
// so without normalization every call_tool-mode turn would score 0 even when the
// model chose the correct tool. This compare rewrites each actual `call_tool`
// entry's Name to its arguments.tool_name, then delegates to the standard
// subset + regex matcher. For every other mode (none/keyword/embedding) there is
// no `call_tool` in the trajectory, so the normalization is a no-op and behavior
// is identical to the built-in criterion.
func unwrapCallToolCompare() tooltrajectory.CompareFunc {
	// Inner criterion replicates the JSON metric: regex name match, subset,
	// unordered. Keep this in sync with the metrics file.
	inner := tooltrajectory.New(
		tooltrajectory.WithDefault(&tooltrajectory.ToolTrajectoryStrategy{
			Name: &text.TextCriterion{MatchStrategy: text.TextMatchStrategyRegex},
		}),
		tooltrajectory.WithSubsetMatching(true),
	)
	return func(actual, expected *evalset.Invocation) (bool, error) {
		return inner.Match(normalizeCallTool(actual), expected)
	}
}

// normalizeCallTool returns a shallow copy of inv whose `call_tool` entries have
// their Name replaced by arguments.tool_name. The input is not mutated. A nil
// input or an entry without a resolvable tool_name is passed through unchanged.
func normalizeCallTool(inv *evalset.Invocation) *evalset.Invocation {
	if inv == nil || len(inv.Tools) == 0 {
		return inv
	}
	out := *inv
	out.Tools = make([]*evalset.Tool, len(inv.Tools))
	for i, t := range inv.Tools {
		if t == nil {
			continue
		}
		if t.Name != "call_tool" {
			out.Tools[i] = t
			continue
		}
		name := toolNameFromArgs(t.Arguments)
		if name == "" {
			out.Tools[i] = t
			continue
		}
		clone := *t
		clone.Name = name
		out.Tools[i] = &clone
	}
	return &out
}

// toolNameFromArgs pulls "tool_name" out of a call_tool arguments payload.
// Arguments arrive as a decoded map[string]any (see parseToolCallArguments in the
// evaluation inference path).
func toolNameFromArgs(args any) string {
	m, ok := args.(map[string]any)
	if !ok {
		return ""
	}
	name, _ := m["tool_name"].(string)
	return strings.TrimSpace(name)
}
