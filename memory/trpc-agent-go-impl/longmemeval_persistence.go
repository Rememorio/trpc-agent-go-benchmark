//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package main

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
)

const (
	lmePersistenceObserved         = "observed"
	lmePersistenceAlreadySatisfied = "already_satisfied"
	lmePersistenceNotObserved      = "not_observed"
	lmePersistenceUnverifiable     = "unverifiable"
)

type extractionPersistenceTrace struct {
	OperationIndex      int                     `json:"operation_index"`
	Stage               string                  `json:"stage,omitempty"`
	Type                extractor.OperationType `json:"type"`
	Status              string                  `json:"status"`
	Effect              string                  `json:"effect,omitempty"`
	Reason              string                  `json:"reason"`
	TargetMemoryID      string                  `json:"target_memory_id,omitempty"`
	ObservedMemoryID    string                  `json:"observed_memory_id,omitempty"`
	ObservedAttribution string                  `json:"observed_attribution,omitempty"`
}

func unverifiableExtractionPersistence(
	extraction *extractionTrace,
	reason string,
) []extractionPersistenceTrace {
	if extraction == nil || len(extraction.Operations) == 0 {
		return nil
	}
	return unverifiableOperationPersistence(extraction.Operations, reason)
}

func unverifiableOperationPersistence(
	operations []extractionOperation,
	reason string,
) []extractionPersistenceTrace {
	out := make(
		[]extractionPersistenceTrace,
		len(operations),
	)
	for index, operation := range operations {
		out[index] = extractionPersistenceTrace{
			OperationIndex: index,
			Stage:          operation.Stage,
			Type:           operation.Type,
			Status:         lmePersistenceUnverifiable,
			Reason:         reason,
			TargetMemoryID: operation.MemoryID,
		}
	}
	return out
}

func traceExtractionPersistence(
	extraction *extractionTrace,
	before []memorySnapshot,
	after []memorySnapshot,
	changed []memorySnapshot,
	beforeSnapshotTruncated bool,
	afterSnapshotTruncated bool,
) []extractionPersistenceTrace {
	if extraction == nil || len(extraction.Operations) == 0 {
		return nil
	}
	return traceOperationPersistence(
		extraction.Operations,
		before,
		after,
		changed,
		beforeSnapshotTruncated,
		afterSnapshotTruncated,
	)
}

func traceOperationPersistence(
	operations []extractionOperation,
	before []memorySnapshot,
	after []memorySnapshot,
	changed []memorySnapshot,
	beforeSnapshotTruncated bool,
	afterSnapshotTruncated bool,
) []extractionPersistenceTrace {
	out := make(
		[]extractionPersistenceTrace,
		0,
		len(operations),
	)
	consumedChanges := make([]bool, len(changed))
	for index, operation := range operations {
		result := extractionPersistenceTrace{
			OperationIndex: index,
			Stage:          operation.Stage,
			Type:           operation.Type,
			TargetMemoryID: operation.MemoryID,
		}
		switch operation.Type {
		case extractor.OperationAdd, extractor.OperationUpdate:
			traceAddOrUpdatePersistence(
				&result,
				operation,
				before,
				after,
				changed,
				consumedChanges,
				beforeSnapshotTruncated,
				afterSnapshotTruncated,
			)
		case extractor.OperationDelete:
			traceDeletePersistence(
				&result,
				operation,
				before,
				after,
				beforeSnapshotTruncated,
				afterSnapshotTruncated,
			)
		case extractor.OperationClear:
			traceClearPersistence(
				&result,
				before,
				after,
				beforeSnapshotTruncated,
				afterSnapshotTruncated,
			)
		default:
			result.Status = lmePersistenceUnverifiable
			result.Reason = "unsupported_operation"
		}
		out = append(out, result)
	}
	return out
}

func traceAddOrUpdatePersistence(
	result *extractionPersistenceTrace,
	operation extractionOperation,
	before []memorySnapshot,
	after []memorySnapshot,
	changed []memorySnapshot,
	consumedChanges []bool,
	beforeSnapshotTruncated bool,
	afterSnapshotTruncated bool,
) {
	memoryText := strings.TrimSpace(operation.Memory)
	if memoryText == "" {
		result.Status = lmePersistenceUnverifiable
		result.Reason = "empty_operation_memory"
		return
	}
	if changedIndex, snapshot, ok := findUnconsumedSnapshotForOperation(
		changed,
		consumedChanges,
		operation,
	); ok {
		consumedChanges[changedIndex] = true
		result.Status = lmePersistenceObserved
		result.Reason = "snapshot_changed"
		result.ObservedMemoryID = snapshot.ID
		result.ObservedAttribution = snapshot.AttributedTo
		if operation.Type == extractor.OperationUpdate {
			if _, targetExisted := findSnapshotByID(
				before,
				strings.TrimSpace(operation.MemoryID),
			); targetExisted {
				result.Effect = string(extractor.OperationUpdate)
			} else if !beforeSnapshotTruncated {
				result.Effect = string(extractor.OperationAdd)
			}
		} else if _, existed := findSnapshotByID(
			before, snapshot.ID,
		); existed {
			result.Effect = string(extractor.OperationUpdate)
		} else if !beforeSnapshotTruncated {
			result.Effect = string(extractor.OperationAdd)
		}
		return
	}
	if snapshot, ok := findSnapshotForOperation(after, operation); ok {
		result.Status = lmePersistenceAlreadySatisfied
		result.Reason = "content_already_present"
		result.ObservedMemoryID = snapshot.ID
		result.ObservedAttribution = snapshot.AttributedTo
		return
	}
	if afterSnapshotTruncated {
		result.Status = lmePersistenceUnverifiable
		result.Reason = "snapshot_truncated"
		return
	}
	result.Status = lmePersistenceNotObserved
	result.Reason = "no_snapshot_effect"
}

func traceDeletePersistence(
	result *extractionPersistenceTrace,
	operation extractionOperation,
	before []memorySnapshot,
	after []memorySnapshot,
	beforeSnapshotTruncated bool,
	afterSnapshotTruncated bool,
) {
	target := strings.TrimSpace(operation.MemoryID)
	if target == "" {
		result.Status = lmePersistenceUnverifiable
		result.Reason = "missing_target_id"
		return
	}
	_, existedBefore := findSnapshotByID(before, target)
	if !existedBefore {
		if beforeSnapshotTruncated {
			result.Status = lmePersistenceUnverifiable
			result.Reason = "before_snapshot_truncated"
			return
		}
		result.Status = lmePersistenceAlreadySatisfied
		result.Reason = "target_already_absent"
		return
	}
	if _, existsAfter := findSnapshotByID(after, target); existsAfter {
		result.Status = lmePersistenceNotObserved
		result.Reason = "target_still_present"
		return
	}
	if afterSnapshotTruncated {
		result.Status = lmePersistenceUnverifiable
		result.Reason = "after_snapshot_truncated"
		return
	}
	result.Status = lmePersistenceObserved
	result.Effect = string(extractor.OperationDelete)
	result.Reason = "target_removed"
	result.ObservedMemoryID = target
}

func traceClearPersistence(
	result *extractionPersistenceTrace,
	before []memorySnapshot,
	after []memorySnapshot,
	beforeSnapshotTruncated bool,
	afterSnapshotTruncated bool,
) {
	if len(before) == 0 {
		if beforeSnapshotTruncated {
			result.Status = lmePersistenceUnverifiable
			result.Reason = "before_snapshot_truncated"
			return
		}
		result.Status = lmePersistenceAlreadySatisfied
		result.Reason = "memory_already_empty"
		return
	}
	if len(after) > 0 {
		result.Status = lmePersistenceNotObserved
		result.Reason = "memories_still_present"
		return
	}
	if afterSnapshotTruncated {
		result.Status = lmePersistenceUnverifiable
		result.Reason = "after_snapshot_truncated"
		return
	}
	result.Status = lmePersistenceObserved
	result.Effect = string(extractor.OperationClear)
	result.Reason = "memory_cleared"
}

func findUnconsumedSnapshotForOperation(
	snapshots []memorySnapshot,
	consumed []bool,
	operation extractionOperation,
) (int, memorySnapshot, bool) {
	match := func(snapshot memorySnapshot) bool {
		return strings.TrimSpace(snapshot.Memory) ==
			strings.TrimSpace(operation.Memory)
	}
	if target := strings.TrimSpace(operation.MemoryID); target != "" {
		for index, snapshot := range snapshots {
			if !consumed[index] && snapshot.ID == target &&
				match(snapshot) {
				return index, snapshot, true
			}
		}
	}
	if attribution := persistenceOperationAttribution(operation.Stage); attribution != "" {
		for index, snapshot := range snapshots {
			if !consumed[index] &&
				snapshot.AttributedTo == attribution &&
				match(snapshot) {
				return index, snapshot, true
			}
		}
	}
	for index, snapshot := range snapshots {
		if !consumed[index] && match(snapshot) {
			return index, snapshot, true
		}
	}
	return -1, memorySnapshot{}, false
}

func findSnapshotForOperation(
	snapshots []memorySnapshot,
	operation extractionOperation,
) (memorySnapshot, bool) {
	if target := strings.TrimSpace(operation.MemoryID); target != "" {
		for _, snapshot := range snapshots {
			if snapshot.ID == target &&
				strings.TrimSpace(snapshot.Memory) ==
					strings.TrimSpace(operation.Memory) {
				return snapshot, true
			}
		}
	}
	if attribution := persistenceOperationAttribution(operation.Stage); attribution != "" {
		for _, snapshot := range snapshots {
			if snapshot.AttributedTo == attribution &&
				strings.TrimSpace(snapshot.Memory) ==
					strings.TrimSpace(operation.Memory) {
				return snapshot, true
			}
		}
	}
	for _, snapshot := range snapshots {
		if strings.TrimSpace(snapshot.Memory) ==
			strings.TrimSpace(operation.Memory) {
			return snapshot, true
		}
	}
	return memorySnapshot{}, false
}

func persistenceOperationAttribution(stage string) string {
	switch strings.TrimSpace(stage) {
	case "ordinary":
		return lmeAttributionUser
	case "assistant_episode":
		return lmeAttributionAssistant
	default:
		return ""
	}
}

func findSnapshotByID(
	snapshots []memorySnapshot,
	memoryID string,
) (memorySnapshot, bool) {
	for _, snapshot := range snapshots {
		if snapshot.ID == memoryID {
			return snapshot, true
		}
	}
	return memorySnapshot{}, false
}
