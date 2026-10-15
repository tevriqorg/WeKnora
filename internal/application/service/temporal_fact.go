package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type TemporalEpisodeRequest struct {
	EpisodeUUID          string    `json:"episode_uuid"`
	GroupID              string    `json:"group_id"`
	Name                 string    `json:"name"`
	Content              string    `json:"content"`
	SourceType           string    `json:"source_type"`
	SourceDescription    string    `json:"source_description"`
	ReferenceTime        time.Time `json:"reference_time"`
	PreviousEpisodeUUIDs []string  `json:"previous_episode_uuids"`
	AuthorityKey         string    `json:"authority_key"`
	GenerationID         string    `json:"generation_id"`
}

type TemporalEpisodeResult struct {
	EpisodeUUID   string              `json:"episode_uuid"`
	FactUUIDs     []string            `json:"fact_uuids"`
	InvalidatedBy map[string][]string `json:"invalidated_by"`
	Status        string              `json:"status"`
	Reused        bool                `json:"reused"`
}

type TemporalFactService struct {
	baseURL string
	client  *http.Client
}

func NewTemporalFactService(baseURL string, client *http.Client) (*TemporalFactService, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("temporal adapter base URL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid temporal adapter base URL: %q", baseURL)
	}
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &TemporalFactService{baseURL: baseURL, client: client}, nil
}

func (s *TemporalFactService) UpsertEpisode(ctx context.Context, input TemporalEpisodeRequest) (*TemporalEpisodeResult, error) {
	if s == nil {
		return nil, errors.New("temporal fact service is nil")
	}
	if input.EpisodeUUID == "" || input.GroupID == "" || input.GenerationID == "" || input.Content == "" {
		return nil, errors.New("temporal episode requires episode_uuid, group_id, generation_id and content")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal temporal episode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/v1/episodes/upsert", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build temporal adapter request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("temporal adapter request: %w", err)
	}
	defer resp.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, fmt.Errorf("read temporal adapter response: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("temporal adapter status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var result TemporalEpisodeResult
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, fmt.Errorf("decode temporal adapter response: %w", err)
	}
	if result.Status != "committed" || result.EpisodeUUID != input.EpisodeUUID {
		return nil, fmt.Errorf("invalid temporal adapter commit response status=%q episode=%q", result.Status, result.EpisodeUUID)
	}
	if result.InvalidatedBy == nil {
		result.InvalidatedBy = map[string][]string{}
	}
	return &result, nil
}
