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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	framework "trpc.group/trpc-go/trpc-agent-go/evolution/optimization"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type fakeRunner struct {
	result  *framework.Result
	err     error
	request *framework.Request
}

func (r *fakeRunner) Optimize(
	_ context.Context,
	request framework.Request,
) (*framework.Result, error) {
	r.request = &request
	return r.result, r.err
}

func TestRun(t *testing.T) {
	seed := testSpec()
	dataset := framework.Dataset{ID: "dataset"}
	want := &framework.Result{Spec: seed}
	fake := &fakeRunner{result: want}
	factory := func(
		_ model.Model,
		_ framework.Evaluator,
		opts ...framework.Option,
	) (runner, error) {
		require.NotEmpty(t, opts)
		return fake, nil
	}
	cfg := Config{
		MaxIterations:   2,
		ReflectionBatch: 1,
		MaxMetricCalls:  10,
		RandomSeed:      7,
		TimeLimit:       time.Second,
	}
	request := Request{
		Config:   cfg,
		Seed:     seed,
		Dataset:  dataset,
		StoreDir: t.TempDir(),
	}
	result, err := run(context.Background(), request, factory)
	require.NoError(t, err)
	require.Same(t, want, result)
	require.Same(t, seed, fake.request.Seed)
	require.Equal(t, dataset.ID, fake.request.Dataset.ID)

	factoryErr := errors.New("factory failed")
	_, err = run(
		context.Background(), request,
		func(model.Model, framework.Evaluator, ...framework.Option) (runner, error) {
			return nil, factoryErr
		},
	)
	require.ErrorIs(t, err, factoryErr)

	fake.err = errors.New("optimize failed")
	_, err = run(context.Background(), request, factory)
	require.ErrorIs(t, err, fake.err)
}
