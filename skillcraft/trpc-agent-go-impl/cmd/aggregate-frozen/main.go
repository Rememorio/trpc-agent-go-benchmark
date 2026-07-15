//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command aggregate-frozen validates and aggregates independent frozen
// SkillCraft comparisons.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go-benchmark/skillcraft/trpc-agent-go-impl/internal/experiment"
)

type inputPaths []string

func (p *inputPaths) String() string { return fmt.Sprint([]string(*p)) }

func (p *inputPaths) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func main() {
	var inputs inputPaths
	var output string
	flag.Var(&inputs, "input", "Path to one frozen optimize results.json; repeat for each seed")
	flag.StringVar(&output, "output", "", "Optional output JSON path (stdout when empty)")
	flag.Parse()

	evidence, err := experiment.AggregateFrozen(inputs, experiment.DefaultFrozenProtocol())
	if err != nil {
		fmt.Fprintf(os.Stderr, "aggregate-frozen: %v\n", err)
		os.Exit(1)
	}
	payload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "aggregate-frozen: marshal evidence: %v\n", err)
		os.Exit(1)
	}
	payload = append(payload, '\n')
	if output == "" {
		_, _ = os.Stdout.Write(payload)
		return
	}
	if err := os.WriteFile(output, payload, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "aggregate-frozen: write evidence: %v\n", err)
		os.Exit(1)
	}
}
