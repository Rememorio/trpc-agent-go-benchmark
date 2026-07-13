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
	"strings"
	"testing"
)

func TestLongMemEvalDateHelpers(t *testing.T) {
	t.Parallel()

	ts, ok := lmeUnixTimestamp("2023/04/10 (Mon) 14:47")
	if !ok {
		t.Fatal("expected date to parse")
	}
	if ts != 1681138020 {
		t.Fatalf("unexpected timestamp: got %d", ts)
	}

	if _, ok := lmeUnixTimestamp("not-a-date"); ok {
		t.Fatal("invalid date parsed")
	}
}

func TestWithObservationDate(t *testing.T) {
	t.Parallel()

	got := withObservationDate("The Fitbit has been used for 9 months.", "2023/04/10 (Mon) 14:47")
	if !strings.HasPrefix(got, "Observation date: 2023/04/10 (Mon) 14:47\n") {
		t.Fatalf("missing observation date prefix: %q", got)
	}
	if !strings.Contains(got, "The Fitbit has been used for 9 months.") {
		t.Fatalf("missing original content: %q", got)
	}
	if out := withObservationDate("content", "  "); out != "content" {
		t.Fatalf("empty date should leave content unchanged: %q", out)
	}
}
