package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const defaultTemporalWindowChars = 12000

var temporalEpisodeNamespace = uuid.MustParse("f17bcd02-4aee-5fd3-9903-609c1001804b")

type temporalLocatorEntry struct {
	ChunkID        string               `json:"chunk_id"`
	StartAt        int                  `json:"start_at"`
	EndAt          int                  `json:"end_at"`
	SegmentStart   int                  `json:"segment_start,omitempty"`
	SegmentEnd     int                  `json:"segment_end,omitempty"`
	SourceLocators types.SourceLocators `json:"source_locators,omitempty"`
}

type temporalEvidenceWindow struct {
	Ordinal      int
	Content      string
	LocatorJSON  types.JSON
	SnapshotHash string
}

func deterministicTemporalEpisodeUUID(knowledgeID, sourceRevisionID, generationID string, ordinal int) (string, error) {
	if strings.TrimSpace(knowledgeID) == "" || strings.TrimSpace(sourceRevisionID) == "" || strings.TrimSpace(generationID) == "" || ordinal < 0 {
		return "", fmt.Errorf("invalid temporal episode identity")
	}
	name := fmt.Sprintf("knowledge=%s\nrevision=%s\ngeneration=%s\nwindow=%d", knowledgeID, sourceRevisionID, generationID, ordinal)
	return uuid.NewSHA1(temporalEpisodeNamespace, []byte(name)).String(), nil
}

func temporalReferenceTime(revision *types.SourceRevision) time.Time {
	if revision == nil {
		return time.Time{}
	}
	if revision.SourceUpdatedAt != nil && !revision.SourceUpdatedAt.IsZero() {
		return revision.SourceUpdatedAt.UTC()
	}
	return revision.ObservedAt.UTC()
}

func buildTemporalEvidenceWindows(chunks []*types.Chunk, maxChars int) ([]temporalEvidenceWindow, error) {
	if maxChars <= 0 {
		maxChars = defaultTemporalWindowChars
	}
	ordered := append([]*types.Chunk(nil), chunks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i] == nil {
			return false
		}
		if ordered[j] == nil {
			return true
		}
		if ordered[i].StartAt != ordered[j].StartAt {
			return ordered[i].StartAt < ordered[j].StartAt
		}
		if ordered[i].ChunkIndex != ordered[j].ChunkIndex {
			return ordered[i].ChunkIndex < ordered[j].ChunkIndex
		}
		return ordered[i].ID < ordered[j].ID
	})

	type piece struct {
		text    string
		locator temporalLocatorEntry
	}
	pieces := make([]piece, 0, len(ordered))
	for _, chunk := range ordered {
		if chunk == nil || !chunk.IsEnabled {
			continue
		}
		text := strings.TrimSpace(chunk.EmbeddingContent())
		if text == "" {
			continue
		}
		runes := []rune(text)
		for start := 0; start < len(runes); start += maxChars {
			end := start + maxChars
			if end > len(runes) {
				end = len(runes)
			}
			pieces = append(pieces, piece{
				text:    string(runes[start:end]),
				locator: temporalLocatorEntry{ChunkID: chunk.ID, StartAt: chunk.StartAt, EndAt: chunk.EndAt, SegmentStart: start, SegmentEnd: end, SourceLocators: chunk.SourceLocators},
			})
		}
	}

	windows := make([]temporalEvidenceWindow, 0)
	var texts []string
	var locators []temporalLocatorEntry
	chars := 0
	flush := func() error {
		if len(texts) == 0 {
			return nil
		}
		content := strings.Join(texts, "\n\n")
		payload, err := json.Marshal(locators)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(content))
		windows = append(windows, temporalEvidenceWindow{Ordinal: len(windows), Content: content, LocatorJSON: types.JSON(payload), SnapshotHash: hex.EncodeToString(sum[:])})
		texts = nil
		locators = nil
		chars = 0
		return nil
	}
	for _, p := range pieces {
		pieceChars := utf8.RuneCountInString(p.text)
		sep := 0
		if len(texts) > 0 {
			sep = 2
		}
		if len(texts) > 0 && chars+sep+pieceChars > maxChars {
			if err := flush(); err != nil {
				return nil, err
			}
			sep = 0
		}
		texts = append(texts, p.text)
		locators = append(locators, p.locator)
		chars += sep + pieceChars
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return windows, nil
}
