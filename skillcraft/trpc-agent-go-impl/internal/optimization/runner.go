//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type runnerFactory func(
	model.Model,
	framework.Evaluator,
	...framework.Option,
) (framework.Optimizer, error)

// Request contains the dependencies and inputs for one reflective search.
type Request struct {
	ReflectionModel model.Model
	Evaluator       framework.Evaluator
	Config          Config
	Seed            *evolution.SkillSpec
	Candidate       *evolution.SkillSpec
	Dataset         framework.Dataset
	StoreDir        string
}

// Outcome contains the common optimizer result and optional per-case frozen
// comparison evidence.
type Outcome struct {
	Search     *framework.Result
	Comparison *Comparison
}

// Run executes reflective search or a frozen candidate comparison. The caller
// owns model usage tracking and benchmark-specific outputs.
func Run(
	ctx context.Context,
	request Request,
) (*Outcome, error) {
	return run(ctx, request, newRunner)
}

func newRunner(
	reflectionModel model.Model,
	evaluator framework.Evaluator,
	opts ...framework.Option,
) (framework.Optimizer, error) {
	return framework.NewGEPA(reflectionModel, evaluator, opts...)
}

func run(
	ctx context.Context,
	request Request,
	newRunner runnerFactory,
) (*Outcome, error) {
	if request.Candidate != nil {
		return runComparison(ctx, request)
	}
	opts := []framework.Option{
		framework.WithMaxIterations(request.Config.MaxIterations),
		framework.WithReflectionBatchSize(request.Config.ReflectionBatch),
		framework.WithMaxMetricCalls(request.Config.MaxMetricCalls),
		framework.WithRandomSeed(request.Config.RandomSeed),
		framework.WithStoreDir(request.StoreDir),
	}
	if request.Config.TimeLimit > 0 {
		opts = append(opts, framework.WithTimeLimit(request.Config.TimeLimit))
	}
	optimizer, err := newRunner(request.ReflectionModel, request.Evaluator, opts...)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	result, err := optimizer.Optimize(ctx, framework.Request{
		Seed:    request.Seed,
		Dataset: request.Dataset,
	})
	if err != nil {
		return nil, err
	}
	return &Outcome{Search: result}, nil
}
