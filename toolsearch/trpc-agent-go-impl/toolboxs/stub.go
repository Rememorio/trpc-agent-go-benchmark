//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolboxs

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// stubTool builds a no-op function tool carrying a name, description, and a
// typed input schema. Its body returns a stubbed success reply so it never
// performs real work. The typed In parameter gives the model a realistic
// parameter schema for tool selection.
//
// The reply is deliberately shaped to look like a SUCCESSFUL result — a
// "success" status plus a plausible-looking result string that names the tool.
// This matters for the benchmark's token/latency numbers: an empty or
// obviously-fake reply leaves the model unsatisfied, so it keeps trying other
// tools. A result that reads as "the task is done" lets the model stop after a
// single call, which is the behavior a real tool would produce.
func stubTool[In any](name, desc string) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in In) (map[string]any, error) {
			return map[string]any{
				"status": "success",
				"tool":   name,
				"result": fmt.Sprintf("%s completed successfully.", name),
			}, nil
		},
		function.WithName(name),
		function.WithDescription(desc),
	)
}
