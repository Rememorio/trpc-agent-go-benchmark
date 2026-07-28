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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

func TestTraceExtractionPersistence(t *testing.T) {
	t.Parallel()

	before := []memorySnapshot{
		{ID: "existing", Memory: "Already stored."},
		{ID: "update", Memory: "Old value."},
		{ID: "delete", Memory: "Delete me."},
	}
	after := []memorySnapshot{
		{ID: "existing", Memory: "Already stored."},
		{ID: "update", Memory: "Updated value."},
		{ID: "added", Memory: "New value.", AttributedTo: lmeAttributionUser},
		{ID: "history", Memory: "Historical value."},
	}
	extraction := &extractionTrace{
		Operations: []extractionOperation{
			{
				Stage:  "primary",
				Type:   extractor.OperationAdd,
				Memory: "New value.",
			},
			{
				Stage:  "primary",
				Type:   extractor.OperationAdd,
				Memory: "Already stored.",
			},
			{
				Stage:    "primary",
				Type:     extractor.OperationUpdate,
				MemoryID: "update",
				Memory:   "Updated value.",
			},
			{
				Stage:    "primary",
				Type:     extractor.OperationUpdate,
				MemoryID: "missing-history-target",
				Memory:   "Historical value.",
			},
			{
				Stage:    "primary",
				Type:     extractor.OperationDelete,
				MemoryID: "delete",
			},
			{
				Stage:    "primary",
				Type:     extractor.OperationDelete,
				MemoryID: "already-absent",
			},
			{Stage: "primary", Type: extractor.OperationClear},
			{Stage: "primary", Type: extractor.OperationType("custom")},
			{
				Stage:  "primary",
				Type:   extractor.OperationAdd,
				Memory: "Not persisted.",
			},
			{
				Stage:  "primary",
				Type:   extractor.OperationAdd,
				Memory: "New value.",
			},
		},
	}

	got := traceExtractionPersistence(
		extraction,
		before,
		after,
		diffSnapshots(before, after),
		false,
		false,
	)
	want := []extractionPersistenceTrace{
		{
			Status:              lmePersistenceObserved,
			Effect:              string(extractor.OperationAdd),
			Reason:              "snapshot_changed",
			ObservedMemoryID:    "added",
			ObservedAttribution: lmeAttributionUser,
		},
		{
			Status:           lmePersistenceAlreadySatisfied,
			Reason:           "content_already_present",
			ObservedMemoryID: "existing",
		},
		{
			Status:           lmePersistenceObserved,
			Effect:           string(extractor.OperationUpdate),
			Reason:           "snapshot_changed",
			ObservedMemoryID: "update",
		},
		{
			Status:           lmePersistenceObserved,
			Effect:           string(extractor.OperationAdd),
			Reason:           "snapshot_changed",
			ObservedMemoryID: "history",
		},
		{
			Status:           lmePersistenceObserved,
			Effect:           string(extractor.OperationDelete),
			Reason:           "target_removed",
			ObservedMemoryID: "delete",
		},
		{
			Status: lmePersistenceAlreadySatisfied,
			Reason: "target_already_absent",
		},
		{
			Status: lmePersistenceNotObserved,
			Reason: "memories_still_present",
		},
		{
			Status: lmePersistenceUnverifiable,
			Reason: "unsupported_operation",
		},
		{
			Status: lmePersistenceNotObserved,
			Reason: "no_snapshot_effect",
		},
		{
			Status:              lmePersistenceAlreadySatisfied,
			Reason:              "content_already_present",
			ObservedMemoryID:    "added",
			ObservedAttribution: lmeAttributionUser,
		},
	}
	if len(got) != len(want) {
		t.Fatalf("persistence outcomes = %d, want %d: %+v",
			len(got), len(want), got)
	}
	for index := range want {
		if got[index].OperationIndex != index ||
			got[index].Stage != extraction.Operations[index].Stage ||
			got[index].Type != extraction.Operations[index].Type ||
			got[index].Status != want[index].Status ||
			got[index].Effect != want[index].Effect ||
			got[index].Reason != want[index].Reason ||
			got[index].ObservedMemoryID != want[index].ObservedMemoryID ||
			got[index].ObservedAttribution !=
				want[index].ObservedAttribution {
			t.Fatalf("persistence outcome %d = %+v, want %+v",
				index, got[index], want[index])
		}
	}
}

func TestUnverifiableExtractionPersistence(t *testing.T) {
	t.Parallel()

	if got := unverifiableExtractionPersistence(nil, "read_error"); got != nil {
		t.Fatalf("nil extraction persistence = %+v", got)
	}
	extraction := &extractionTrace{Operations: []extractionOperation{
		{
			Stage:    "primary",
			Type:     extractor.OperationUpdate,
			MemoryID: "target",
		},
	}}
	got := unverifiableExtractionPersistence(
		extraction,
		"snapshot_read_error",
	)
	if len(got) != 1 ||
		got[0].OperationIndex != 0 ||
		got[0].Stage != "primary" ||
		got[0].Type != extractor.OperationUpdate ||
		got[0].TargetMemoryID != "target" ||
		got[0].Status != lmePersistenceUnverifiable ||
		got[0].Reason != "snapshot_read_error" {
		t.Fatalf("unverifiable persistence = %+v", got)
	}

	extraction.PostPolicyObserved = true
	extraction.PostPolicyOperations = []extractionOperation{{
		Stage:  "assistant_result",
		Type:   extractor.OperationAdd,
		Memory: "Assistant result.",
	}}
	postPolicy := unverifiablePostPolicyPersistence(
		extraction,
		"snapshot_read_error",
	)
	if len(postPolicy) != 1 ||
		postPolicy[0].Stage != "assistant_result" ||
		postPolicy[0].Type != extractor.OperationAdd ||
		postPolicy[0].Reason != "snapshot_read_error" {
		t.Fatalf("post-policy unverifiable persistence = %+v", postPolicy)
	}
}

func TestTracePostPolicyPersistenceRecognizesRotatedUpdate(t *testing.T) {
	t.Parallel()

	extraction := &extractionTrace{
		PostPolicyObserved: true,
		PostPolicyOperations: []extractionOperation{{
			Stage:    "primary",
			Type:     extractor.OperationUpdate,
			MemoryID: "old-id",
			Memory:   "Updated value.",
		}},
	}
	before := []memorySnapshot{{
		ID: "old-id", Memory: "Old value.",
	}}
	after := []memorySnapshot{{
		ID: "new-id", Memory: "Updated value.",
	}}
	got := tracePostPolicyPersistence(
		extraction,
		before,
		after,
		diffSnapshots(before, after),
		false,
		false,
	)
	if len(got) != 1 ||
		got[0].Status != lmePersistenceObserved ||
		got[0].Effect != string(extractor.OperationUpdate) ||
		got[0].TargetMemoryID != "old-id" ||
		got[0].ObservedMemoryID != "new-id" {
		t.Fatalf("rotated update persistence = %+v", got)
	}
}

func TestTraceExtractionPersistenceConservativeReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		operation       extractionOperation
		before          []memorySnapshot
		after           []memorySnapshot
		beforeTruncated bool
		afterTruncated  bool
		wantStatus      string
		wantReason      string
	}{
		{
			name:       "empty add",
			operation:  extractionOperation{Type: extractor.OperationAdd},
			wantStatus: lmePersistenceUnverifiable,
			wantReason: "empty_operation_memory",
		},
		{
			name: "missing add in truncated snapshot",
			operation: extractionOperation{
				Type: extractor.OperationAdd, Memory: "Missing.",
			},
			afterTruncated: true,
			wantStatus:     lmePersistenceUnverifiable,
			wantReason:     "snapshot_truncated",
		},
		{
			name: "delete without target",
			operation: extractionOperation{
				Type: extractor.OperationDelete,
			},
			wantStatus: lmePersistenceUnverifiable,
			wantReason: "missing_target_id",
		},
		{
			name: "delete target still present",
			operation: extractionOperation{
				Type: extractor.OperationDelete, MemoryID: "target",
			},
			before:     []memorySnapshot{{ID: "target"}},
			after:      []memorySnapshot{{ID: "target"}},
			wantStatus: lmePersistenceNotObserved,
			wantReason: "target_still_present",
		},
		{
			name: "delete with truncated after snapshot",
			operation: extractionOperation{
				Type: extractor.OperationDelete, MemoryID: "target",
			},
			before:         []memorySnapshot{{ID: "target"}},
			afterTruncated: true,
			wantStatus:     lmePersistenceUnverifiable,
			wantReason:     "after_snapshot_truncated",
		},
		{
			name:       "clear already empty",
			operation:  extractionOperation{Type: extractor.OperationClear},
			wantStatus: lmePersistenceAlreadySatisfied,
			wantReason: "memory_already_empty",
		},
		{
			name:      "clear with truncated after snapshot",
			operation: extractionOperation{Type: extractor.OperationClear},
			before:    []memorySnapshot{{ID: "existing"}},

			afterTruncated: true,
			wantStatus:     lmePersistenceUnverifiable,
			wantReason:     "after_snapshot_truncated",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := traceExtractionPersistence(
				&extractionTrace{
					Operations: []extractionOperation{test.operation},
				},
				test.before,
				test.after,
				diffSnapshots(test.before, test.after),
				test.beforeTruncated,
				test.afterTruncated,
			)
			if len(got) != 1 ||
				got[0].Status != test.wantStatus ||
				got[0].Reason != test.wantReason {
				t.Fatalf("persistence outcome = %+v, want %s/%s",
					got, test.wantStatus, test.wantReason)
			}
		})
	}

	if got := traceExtractionPersistence(
		&extractionTrace{}, nil, nil, nil, false, false,
	); got != nil {
		t.Fatalf("empty extraction persistence = %+v", got)
	}
}

func TestTraceExtractionPersistenceHandlesTruncatedSnapshots(t *testing.T) {
	t.Parallel()

	extraction := &extractionTrace{
		Operations: []extractionOperation{
			{Type: extractor.OperationAdd, Memory: "Missing value."},
			{Type: extractor.OperationDelete, MemoryID: "missing"},
			{Type: extractor.OperationClear},
		},
	}
	got := traceExtractionPersistence(
		extraction,
		nil,
		nil,
		nil,
		true,
		true,
	)
	for index, outcome := range got {
		if outcome.Status != lmePersistenceUnverifiable {
			t.Fatalf("truncated outcome %d = %+v", index, outcome)
		}
	}
	if got[0].Reason != "snapshot_truncated" ||
		got[1].Reason != "before_snapshot_truncated" ||
		got[2].Reason != "before_snapshot_truncated" {
		t.Fatalf("truncated reasons = %+v", got)
	}
}

func TestTraceExtractionPersistenceDoesNotInferAddFromTruncatedBefore(t *testing.T) {
	t.Parallel()

	after := []memorySnapshot{{
		ID: "possibly-existing", Memory: "Changed value.",
	}}
	got := traceExtractionPersistence(
		&extractionTrace{Operations: []extractionOperation{{
			Type: extractor.OperationAdd, Memory: "Changed value.",
		}}},
		nil,
		after,
		after,
		true,
		false,
	)
	if len(got) != 1 ||
		got[0].Status != lmePersistenceObserved ||
		got[0].Effect != "" {
		t.Fatalf("truncated before snapshot outcome = %+v", got)
	}
}

func TestTraceExtractionPersistencePrefersTargetAndAttribution(t *testing.T) {
	t.Parallel()

	shared := []memorySnapshot{
		{
			ID:           "user",
			Memory:       "Shared text.",
			AttributedTo: lmeAttributionUser,
		},
		{
			ID:           "assistant",
			Memory:       "Shared text.",
			AttributedTo: lmeAttributionAssistant,
		},
	}
	staged := traceExtractionPersistence(
		&extractionTrace{Operations: []extractionOperation{
			{
				Stage:  "assistant_result",
				Type:   extractor.OperationAdd,
				Memory: "Shared text.",
			},
			{
				Stage:  "primary",
				Type:   extractor.OperationAdd,
				Memory: "Shared text.",
			},
		}},
		nil,
		shared,
		shared,
		false,
		false,
	)
	if len(staged) != 2 ||
		staged[0].ObservedMemoryID != "assistant" ||
		staged[1].ObservedMemoryID != "user" {
		t.Fatalf("stage attribution matches = %+v", staged)
	}

	before := []memorySnapshot{{
		ID: "target", Memory: "Old text.",
	}}
	after := []memorySnapshot{
		{ID: "other", Memory: "Updated text."},
		{ID: "target", Memory: "Updated text."},
	}
	targeted := traceExtractionPersistence(
		&extractionTrace{Operations: []extractionOperation{{
			Stage:    "primary",
			Type:     extractor.OperationUpdate,
			MemoryID: "target",
			Memory:   "Updated text.",
		}}},
		before,
		after,
		diffSnapshots(before, after),
		false,
		false,
	)
	if len(targeted) != 1 ||
		targeted[0].ObservedMemoryID != "target" ||
		targeted[0].Effect != string(extractor.OperationUpdate) {
		t.Fatalf("target-id match = %+v", targeted)
	}
}
