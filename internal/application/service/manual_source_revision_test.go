package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManualSourceRevisionCandidateUsesContentFingerprintAndStableLocator(t *testing.T) {
	observed := time.Date(2026, 10, 16, 1, 0, 0, 0, time.FixedZone("SGT", 8*60*60))
	rev := manualSourceRevisionCandidate("kn-1", "# v2\nAtlasNet", observed)
	require.Equal(t, "kn-1", rev.KnowledgeID)
	require.Equal(t, sha256Hex([]byte("# v2\nAtlasNet")), rev.SourceHash)
	require.Equal(t, "manual:kn-1", rev.Locator)
	require.Equal(t, "manual", rev.Mime)
	require.Equal(t, observed.UTC(), rev.ObservedAt)
}
