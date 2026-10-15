package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func temporalRequest() TemporalEpisodeRequest {
	return TemporalEpisodeRequest{
		EpisodeUUID: "ep-1", GroupID: "group-1", Name: "window-0", Content: "Alice works at Acme",
		SourceType: "text", SourceDescription: "source", ReferenceTime: time.Now().UTC(),
		AuthorityKey: "source:a", GenerationID: "gen-1",
	}
}

func TestTemporalFactServiceUpsertPreservesTransitionProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/episodes/upsert", r.URL.Path)
		var req TemporalEpisodeRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "ep-1", req.EpisodeUUID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"episode_uuid":"ep-1","fact_uuids":["new"],"invalidated_by":{"old":["new"]},"status":"committed","reused":false}`))
	}))
	defer server.Close()
	service, err := NewTemporalFactService(server.URL, server.Client())
	require.NoError(t, err)
	result, err := service.UpsertEpisode(context.Background(), temporalRequest())
	require.NoError(t, err)
	require.Equal(t, []string{"new"}, result.InvalidatedBy["old"])
	require.False(t, result.Reused)
}

func TestTemporalFactServiceRejectsNonCommittedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"episode_uuid":"ep-1","status":"pending"}`))
	}))
	defer server.Close()
	service, err := NewTemporalFactService(server.URL, server.Client())
	require.NoError(t, err)
	_, err = service.UpsertEpisode(context.Background(), temporalRequest())
	require.Error(t, err)
}

func TestTemporalFactServiceSearchPreservesTemporalEvidence(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/search", r.URL.Path)
		var req TemporalSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, []string{"group-1"}, req.GroupIDs)
		_ = json.NewEncoder(w).Encode(TemporalSearchResponse{
			Facts:     []TemporalSearchFact{{UUID: "f1", Fact: "Alice works at Acme", Relation: "works_at", GroupID: "group-1", Episodes: []string{"ep1"}, CreatedAt: now, Source: TemporalSearchEntity{UUID: "alice"}, Target: TemporalSearchEntity{UUID: "acme"}}},
			Freshness: TemporalSearchFreshness{Mode: "current", EvaluatedAt: now},
		})
	}))
	defer server.Close()
	service, err := NewTemporalFactService(server.URL, server.Client())
	require.NoError(t, err)
	got, err := service.Search(context.Background(), TemporalSearchRequest{Query: "job", GroupIDs: []string{"group-1"}, Mode: "current", Limit: 20})
	require.NoError(t, err)
	require.Len(t, got.Facts, 1)
	require.Equal(t, "f1", got.Facts[0].UUID)
	require.Equal(t, []string{"ep1"}, got.Facts[0].Episodes)
}
