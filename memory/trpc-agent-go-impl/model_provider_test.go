//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestOpenAIModelOptionsForVariant(t *testing.T) {
	for _, variant := range []string{
		"", "openai", "deepseek", "hunyuan", "qwen", "glm", " GLM ",
	} {
		if _, err := openAIModelOptionsForVariant(variant); err != nil {
			t.Fatalf("variant %q returned error: %v", variant, err)
		}
	}
	if _, err := openAIModelOptionsForVariant("unknown"); err == nil {
		t.Fatal("expected error for unsupported variant")
	}
}
