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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestMem0OSSBackendPreservesAttribution(t *testing.T) {
	t.Parallel()

	var searchRequest struct {
		Query   string         `json:"query"`
		Filters map[string]any `json:"filters"`
		TopK    int            `json:"top_k"`
	}
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/search":
				if err := json.NewDecoder(r.Body).Decode(&searchRequest); err != nil {
					t.Errorf("decode search request: %v", err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(`{"results":[{
					"id":"search-1",
					"memory":"Assistant supplied Option B.",
					"attributed_to":"assistant",
					"score":0.91,
					"created_at":"2024-01-02T03:04:05Z",
					"updated_at":"2024-01-03T03:04:05Z"
				}]}`))
			case r.Method == http.MethodGet && r.URL.Path == "/memories":
				if got := r.URL.Query().Get("user_id"); got != "user-1" {
					t.Errorf("user_id = %q, want user-1", got)
				}
				if got := r.URL.Query().Get("top_k"); got != "1000" {
					t.Errorf("top_k = %q, want 1000", got)
				}
				_, _ = w.Write([]byte(`{"results":[
					{
						"id":"memory-1",
						"memory":"Assistant supplied Option B.",
						"attributed_to":"assistant"
					},
					{
						"id":"memory-2",
						"memory":"User prefers Option B.",
						"metadata":{"attributed_to":"user"}
					}
				]}`))
			default:
				http.NotFound(w, r)
			}
		},
	))
	defer server.Close()

	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
		usage:      &lmeProviderUsageTracker{},
	}
	userKey := memory.UserKey{AppName: "app-1", UserID: "user-1"}

	hits, err := backend.Search(context.Background(), userKey, "Which option?", 30)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].AttributedTo != lmeAttributionAssistant ||
		hits[0].Score != 0.91 || hits[0].CreatedAt.IsZero() ||
		hits[0].UpdatedAt.IsZero() {
		t.Fatalf("search hits = %#v", hits)
	}
	if searchRequest.Query != "Which option?" ||
		searchRequest.TopK != 30 ||
		searchRequest.Filters["user_id"] != "user-1" ||
		searchRequest.Filters[lmeMem0OSSAppNameKey] != "app-1" {
		t.Fatalf("search request = %#v", searchRequest)
	}

	snapshots, truncated, err := backend.Read(context.Background(), userKey)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if truncated || len(snapshots) != 2 ||
		snapshots[0].AttributedTo != lmeAttributionAssistant ||
		snapshots[1].AttributedTo != lmeAttributionUser {
		t.Fatalf("snapshots = %#v, truncated = %v", snapshots, truncated)
	}
}

func TestMem0OSSBackendRejectsMissingAttribution(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"results":[{"id":"memory-1","memory":"Unattributed"}]}`,
			))
		},
	))
	defer server.Close()

	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: server.Client(),
		usage:      &lmeProviderUsageTracker{},
	}
	_, err := backend.Search(
		context.Background(),
		memory.UserKey{AppName: "app-1", UserID: "user-1"},
		"query",
		30,
	)
	if err == nil || !strings.Contains(err.Error(), "attributed_to is required") {
		t.Fatalf("search error = %v", err)
	}
}

func TestMem0OSSBackendRecordsProviderUsage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(lmeMem0UsageHeader, `{
				"llm":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"llm_calls":1},
				"embedding":{"prompt_tokens":3,"total_tokens":3,"calls":1}
			}`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[{
				"id":"memory-1",
				"memory":"Assistant supplied Option B.",
				"attributed_to":"assistant",
				"score":0.8
			}]}`))
		},
	))
	defer server.Close()

	tracker := &lmeProviderUsageTracker{}
	backend := &mem0Backend{
		host:       server.URL,
		selfHosted: true,
		httpClient: &http.Client{Transport: &lmeMem0UsageTransport{
			base:    server.Client().Transport,
			tracker: tracker,
		}},
		usage: tracker,
	}
	_, err := backend.Search(
		context.Background(),
		memory.UserKey{AppName: "app-1", UserID: "user-1"},
		"query",
		30,
	)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	usage := backend.SnapshotProviderUsage()
	if !usage.Reported || usage.LLM.TotalTokens != 12 ||
		usage.Embedding.TotalTokens != 3 {
		t.Fatalf("provider usage = %#v", usage)
	}
}
