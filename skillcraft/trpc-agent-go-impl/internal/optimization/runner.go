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

type runner interface {
	Optimize(context.Context, framework.Request) (*framework.Result, error)
}

type runnerFactory func(
	model.Model,
	framework.Evaluator,
	...framework.Option,
) (runner, error)

// Request contains the dependencies and inputs for one reflective search.
type Request struct {
	ReflectionModel model.Model
	Evaluator       framework.Evaluator
	Config          Config
	Seed            *evolution.SkillSpec
	Dataset         framework.Dataset
	StoreDir        string
}

// Run executes reflective search with the configured budgets and experiment
// store. The caller owns model usage tracking and benchmark-specific outputs.
func Run(
	ctx context.Context,
	request Request,
) (*framework.Result, error) {
	return run(ctx, request, newRunner)
}

func newRunner(
	reflectionModel model.Model,
	evaluator framework.Evaluator,
	opts ...framework.Option,
) (runner, error) {
	return framework.New(reflectionModel, evaluator, opts...)
}

func run(
	ctx context.Context,
	request Request,
	newRunner runnerFactory,
) (*framework.Result, error) {
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
	return optimizer.Optimize(ctx, framework.Request{
		Seed:    request.Seed,
		Dataset: request.Dataset,
	})
}
