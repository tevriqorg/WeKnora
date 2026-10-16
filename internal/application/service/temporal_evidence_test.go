package service

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDeterministicTemporalEpisodeUUIDMatchesAdapterContract(t *testing.T) {
	got, err := deterministicTemporalEpisodeUUID("knowledge-1", "revision-1", "generation-1", 0)
	require.NoError(t, err)
	require.Equal(t, "cf1f1911-b5ca-5007-8b9e-90d3db4f0d24", got)
}

func TestTemporalReferenceTimePrefersSourceUpdatedAt(t *testing.T) {
	observed := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	updated := observed.Add(-2 * time.Hour)
	require.Equal(t, updated, temporalReferenceTime(&types.SourceRevision{ObservedAt: observed, SourceUpdatedAt: &updated}))
	require.Equal(t, observed, temporalReferenceTime(&types.SourceRevision{ObservedAt: observed}))
}

func TestBuildTemporalEvidenceWindowsIsBoundedOrderedAndStable(t *testing.T) {
	chunks := []*types.Chunk{
		{ID: "c2", StartAt: 20, ChunkIndex: 2, Content: "BBBB", IsEnabled: true},
		{ID: "c1", StartAt: 10, ChunkIndex: 1, Content: "AAAAAA", IsEnabled: true},
	}
	first, err := buildTemporalEvidenceWindows(chunks, 8)
	require.NoError(t, err)
	second, err := buildTemporalEvidenceWindows(chunks, 8)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, 2)
	for i, window := range first {
		require.Equal(t, i, window.Ordinal)
		require.LessOrEqual(t, utf8.RuneCountInString(window.Content), 8)
		require.NotEmpty(t, window.SnapshotHash)
		require.NotEmpty(t, window.LocatorJSON)
	}
	require.Equal(t, "AAAAAA", first[0].Content)
	require.Equal(t, "BBBB", first[1].Content)
}

func TestBuildTemporalEvidenceWindowsSplitsOversizedChunkDeterministically(t *testing.T) {
	windows, err := buildTemporalEvidenceWindows([]*types.Chunk{{ID: "c1", Content: "abcdefghij", IsEnabled: true}}, 4)
	require.NoError(t, err)
	require.Len(t, windows, 3)
	require.Equal(t, "abcd", windows[0].Content)
	require.Equal(t, "efgh", windows[1].Content)
	require.Equal(t, "ij", windows[2].Content)
}
