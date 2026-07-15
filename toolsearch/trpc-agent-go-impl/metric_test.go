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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// inv builds an invocation from (name, args) pairs for terse test cases.
func inv(tools ...*evalset.Tool) *evalset.Invocation {
	return &evalset.Invocation{Tools: tools}
}

func tl(name string, args any) *evalset.Tool {
	return &evalset.Tool{Name: name, Arguments: args}
}

func TestUnwrapCallToolCompare(t *testing.T) {
	cmp := unwrapCallToolCompare()

	cases := []struct {
		name     string
		actual   *evalset.Invocation
		expected *evalset.Invocation
		want     bool
	}{
		{
			name:     "keyword mode: tool_search then direct call, subset match",
			actual:   inv(tl("tool_search", nil), tl("delete_user", nil)),
			expected: inv(tl("delete_user", nil)),
			want:     true,
		},
		{
			name:     "call_tool mode: real name lives in arguments.tool_name",
			actual:   inv(tl("tool_search", nil), tl("call_tool", map[string]any{"tool_name": "delete_user"})),
			expected: inv(tl("delete_user", nil)),
			want:     true,
		},
		{
			name:     "call_tool mode: wrong tool unwrapped, no match",
			actual:   inv(tl("tool_search", nil), tl("call_tool", map[string]any{"tool_name": "delete_customer"})),
			expected: inv(tl("delete_user", nil)),
			want:     false,
		},
		{
			name:     "regex alternation accepts either branch",
			actual:   inv(tl("call_tool", map[string]any{"tool_name": "find_files"})),
			expected: inv(tl("search_file_content|find_files", nil)),
			want:     true,
		},
		{
			name:     "none mode: direct call, exact",
			actual:   inv(tl("write_file", nil)),
			expected: inv(tl("write_file", nil)),
			want:     true,
		},
		{
			name:     "call_tool without resolvable tool_name falls back to literal name (no match)",
			actual:   inv(tl("call_tool", map[string]any{})),
			expected: inv(tl("write_file", nil)),
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cmp(tc.actual, tc.expected)
			if err != nil && tc.want {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("compare = %v, want %v (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestNormalizeCallToolDoesNotMutateInput(t *testing.T) {
	orig := inv(tl("call_tool", map[string]any{"tool_name": "delete_user"}))
	_ = normalizeCallTool(orig)
	if orig.Tools[0].Name != "call_tool" {
		t.Fatalf("input mutated: got %q, want %q", orig.Tools[0].Name, "call_tool")
	}
}

func TestToolNameFromArgs(t *testing.T) {
	if got := toolNameFromArgs(map[string]any{"tool_name": " delete_user "}); got != "delete_user" {
		t.Fatalf("got %q, want trimmed delete_user", got)
	}
	if got := toolNameFromArgs("not-a-map"); got != "" {
		t.Fatalf("got %q, want empty for non-map args", got)
	}
	if got := toolNameFromArgs(nil); got != "" {
		t.Fatalf("got %q, want empty for nil args", got)
	}
}

func TestRelativizeToBenchmark(t *testing.T) {
	cases := map[string]string{
		"/Users/x/GolandProjects/trpc-agent-go-benchmark/toolsearch/data":   "toolsearch/data",
		"/Users/x/GolandProjects/trpc-agent-go-benchmark/toolsearch/output": "toolsearch/output",
		"../data":    "data",
		"../output":  "output",
		"toolsearch": "toolsearch",
		"":           "",
	}
	for in, want := range cases {
		if got := relativizeToBenchmark(in); got != want {
			t.Errorf("relativizeToBenchmark(%q) = %q, want %q", in, got, want)
		}
	}
}
