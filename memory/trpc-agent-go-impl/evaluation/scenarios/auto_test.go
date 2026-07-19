//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestAutoExtractionWaitTimeout(t *testing.T) {
	if got := autoExtractionWaitTimeout(19, 20*time.Minute); got != 20*time.Minute {
		t.Fatalf("configured timeout = %s, want 20m", got)
	}
	if got := autoExtractionWaitTimeout(19, 0); got != 19*time.Minute {
		t.Fatalf("derived timeout = %s, want 19m", got)
	}
	if got := autoExtractionWaitTimeout(1, 0); got != 5*time.Minute {
		t.Fatalf("minimum timeout = %s, want 5m", got)
	}
	if got := autoExtractionWaitTimeout(200, 0); got != 60*time.Minute {
		t.Fatalf("maximum timeout = %s, want 60m", got)
	}
}

func TestWaitForAutoExtraction(t *testing.T) {
	first := autoExtractionTestSession("first", time.Now().UTC())
	firstWant := latestAutoExtractionTimestamp(first)
	first.SetState(
		memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(firstWant.Format(time.RFC3339Nano)),
	)
	final := autoExtractionTestSession(
		"final", time.Now().UTC().Add(time.Second),
	)
	want := latestAutoExtractionTimestamp(final)
	final.SetState(
		memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(want.Format(time.RFC3339Nano)),
	)

	if err := waitForAutoExtraction(
		context.Background(),
		[]*session.Session{first, final},
		time.Second,
	); err != nil {
		t.Fatalf("wait for extraction: %v", err)
	}
}

func TestWaitForAutoExtractionChecksEarlierErrors(t *testing.T) {
	first := autoExtractionTestSession("first", time.Now().UTC())
	first.SetState(autoMemoryLastErrorStateKey, []byte("embedding failed"))
	final := autoExtractionTestSession(
		"final", time.Now().UTC().Add(time.Second),
	)
	want := latestAutoExtractionTimestamp(final)
	final.SetState(
		memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(want.Format(time.RFC3339Nano)),
	)

	err := waitForAutoExtraction(
		context.Background(),
		[]*session.Session{first, final},
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "embedding failed") {
		t.Fatalf("error = %v, want earlier extraction failure", err)
	}
}

func TestWaitForAutoExtractionRejectsInvalidMarker(t *testing.T) {
	final := autoExtractionTestSession("final", time.Now().UTC())
	final.SetState(
		memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte("invalid"),
	)

	err := waitForAutoExtraction(
		context.Background(),
		[]*session.Session{final},
		time.Second,
	)
	if err == nil || !strings.Contains(err.Error(), "completion marker") {
		t.Fatalf("error = %v, want invalid completion marker", err)
	}
}

func TestWaitForAutoExtractionTimesOut(t *testing.T) {
	final := autoExtractionTestSession("final", time.Now().UTC())
	err := waitForAutoExtraction(
		context.Background(),
		[]*session.Session{final},
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want timeout", err)
	}
}

func TestLatestAutoExtractionTimestamp(t *testing.T) {
	sess := session.NewSession(autoAppName, "user", "session")
	earlier := time.Now().UTC()
	later := earlier.Add(time.Second)
	sess.Events = []event.Event{
		{Timestamp: later},
		{Timestamp: earlier},
	}

	if got := latestAutoExtractionTimestamp(sess); !got.Equal(later) {
		t.Fatalf("latest timestamp = %s, want %s", got, later)
	}
}

func autoExtractionTestSession(
	id string,
	timestamp time.Time,
) *session.Session {
	sess := session.NewSession(autoAppName, "user", id)
	sess.Events = []event.Event{{Timestamp: timestamp}}
	return sess
}
