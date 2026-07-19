//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newEvaluationModel(modelName, variant string) (model.Model, error) {
	opts, err := openAIModelOptionsForVariant(variant)
	if err != nil {
		return nil, err
	}
	return openaimodel.New(modelName, opts...), nil
}

func openAIModelOptionsForVariant(variant string) ([]openaimodel.Option, error) {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "":
		return nil, nil
	case "openai":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantOpenAI)}, nil
	case "deepseek":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantDeepSeek)}, nil
	case "hunyuan":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantHunyuan)}, nil
	case "qwen":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantQwen)}, nil
	case "glm":
		return []openaimodel.Option{openaimodel.WithVariant(openaimodel.VariantGLM)}, nil
	default:
		return nil, fmt.Errorf("unsupported model variant %q", variant)
	}
}
