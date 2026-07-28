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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	lmeMem0OSSAttributionKey = "attributed_to"
	lmeMem0OSSAppNameKey     = "trpc_app_name"
	lmeMem0OSSMaxResponse    = 64 << 20
)

type lmeMem0OSSRecord struct {
	ID           string         `json:"id"`
	Memory       string         `json:"memory"`
	AttributedTo string         `json:"attributed_to,omitempty"`
	Metadata     map[string]any `json:"metadata"`
	Score        float64        `json:"score,omitempty"`
	CreatedAt    string         `json:"created_at,omitempty"`
	UpdatedAt    string         `json:"updated_at,omitempty"`
}

type lmeMem0OSSRecords struct {
	Results []lmeMem0OSSRecord `json:"results"`
}

func (b *mem0Backend) searchMem0OSS(
	ctx context.Context,
	userKey memory.UserKey,
	query string,
	topK int,
) ([]memoryHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []memoryHit{}, nil
	}
	payload := map[string]any{
		"query": query,
		"filters": map[string]any{
			"user_id":            userKey.UserID,
			lmeMem0OSSAppNameKey: userKey.AppName,
		},
		"top_k": topK,
	}
	var response lmeMem0OSSRecords
	if err := b.doMem0OSSJSON(
		ctx, http.MethodPost, "/search", nil, payload, &response,
	); err != nil {
		return nil, err
	}
	hits := make([]memoryHit, 0, len(response.Results))
	for index := range response.Results {
		hit, err := mem0OSSRecordHit(&response.Results[index])
		if err != nil {
			return nil, fmt.Errorf("mem0 OSS search result %d: %w", index, err)
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

func (b *mem0Backend) readMem0OSS(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]memorySnapshot, bool, error) {
	query := url.Values{"user_id": []string{userKey.UserID}}
	if limit > 0 {
		query.Set("top_k", strconv.Itoa(limit))
	}
	var response lmeMem0OSSRecords
	if err := b.doMem0OSSJSON(
		ctx, http.MethodGet, "/memories", query, nil, &response,
	); err != nil {
		return nil, false, err
	}
	snapshots := make([]memorySnapshot, 0, len(response.Results))
	for index := range response.Results {
		snapshot, err := mem0OSSRecordSnapshot(&response.Results[index])
		if err != nil {
			return nil, false,
				fmt.Errorf("mem0 OSS memory %d: %w", index, err)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, limit > 0 && len(snapshots) >= limit, nil
}

func (b *mem0Backend) doMem0OSSJSON(
	ctx context.Context,
	method string,
	path string,
	query url.Values,
	payload any,
	out any,
) error {
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal Mem0 OSS request: %w", err)
		}
	}
	endpoint := strings.TrimRight(b.host, "/") + path
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt <= lmeMem0RequestRetries; attempt++ {
		if attempt > 0 {
			delay := mem0RequestRetryDelay(attempt)
			if err := sleepWithContext(ctx, delay); err != nil {
				return err
			}
		}
		requestCtx, cancel := contextWithOptionalTimeout(
			ctx, longMemEvalMem0OSSRequestTimeout(),
		)
		req, err := http.NewRequestWithContext(
			requestCtx, method, endpoint, bytes.NewReader(body),
		)
		if err != nil {
			cancel()
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("mem0 OSS %s request failed: %w", path, err)
			continue
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(
			resp.Body, lmeMem0OSSMaxResponse+1,
		))
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			return fmt.Errorf("read Mem0 OSS %s response: %w", path, readErr)
		}
		if len(responseBody) > lmeMem0OSSMaxResponse {
			return fmt.Errorf(
				"Mem0 OSS %s response exceeds %d bytes",
				path, lmeMem0OSSMaxResponse,
			)
		}
		if resp.StatusCode < http.StatusOK ||
			resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf(
				"mem0 OSS %s failed: status=%d body=%s",
				path, resp.StatusCode,
				strings.TrimSpace(string(responseBody)),
			)
			if isRetryableMem0Response(resp.StatusCode, responseBody) {
				continue
			}
			return lastErr
		}
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode Mem0 OSS %s response: %w", path, err)
		}
		return nil
	}
	return lastErr
}

func mem0OSSRecordHit(record *lmeMem0OSSRecord) (memoryHit, error) {
	snapshot, err := mem0OSSRecordSnapshot(record)
	if err != nil {
		return memoryHit{}, err
	}
	return memoryHit{
		ID:           snapshot.ID,
		Memory:       snapshot.Memory,
		AttributedTo: snapshot.AttributedTo,
		Score:        record.Score,
		CreatedAt:    snapshot.CreatedAt,
		UpdatedAt:    snapshot.UpdatedAt,
	}, nil
}

func mem0OSSRecordSnapshot(
	record *lmeMem0OSSRecord,
) (memorySnapshot, error) {
	if record == nil {
		return memorySnapshot{}, errors.New("record is nil")
	}
	id := strings.TrimSpace(record.ID)
	text := strings.TrimSpace(record.Memory)
	if id == "" || text == "" {
		return memorySnapshot{}, errors.New("record ID and memory are required")
	}
	attributedTo, err := mem0OSSRecordAttribution(record)
	if err != nil {
		return memorySnapshot{}, err
	}
	return memorySnapshot{
		ID:           id,
		Memory:       text,
		AttributedTo: attributedTo,
		CreatedAt:    parseMem0OSSTime(record.CreatedAt),
		UpdatedAt:    parseMem0OSSTime(record.UpdatedAt),
	}, nil
}

func mem0OSSRecordAttribution(record *lmeMem0OSSRecord) (string, error) {
	value := strings.TrimSpace(record.AttributedTo)
	if value == "" {
		value, _ = record.Metadata[lmeMem0OSSAttributionKey].(string)
	}
	if strings.TrimSpace(value) == "" {
		return "", errors.New("metadata.attributed_to is required")
	}
	attributedTo := normalizeMemoryAttribution(value)
	if attributedTo == "" {
		return "", fmt.Errorf(
			"metadata.attributed_to %q is invalid", value,
		)
	}
	return attributedTo, nil
}

func parseMem0OSSTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
