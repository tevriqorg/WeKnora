package service

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const defaultAIBOXQueryLimit = 20

var aiboxDatePattern = regexp.MustCompile(`\b(20\d{2}-\d{2}-\d{2})(?:T([0-9:]+(?:Z|[+-][0-9:]+)?))?\b`)

type aiboxDocumentSearcher interface {
	SearchKnowledge(context.Context, []string, []string, []types.TagScope, string, *types.KnowledgeSearchOptions) (*types.RetrievalResult, error)
}

type aiboxQueryService struct {
	documents    aiboxDocumentSearcher
	temporalRepo *apprepo.TemporalRepository
	adapterURL   string
}

func NewAIBOXQueryService(session interfaces.SessionService, repo *apprepo.TemporalRepository) interfaces.AIBOXQueryService {
	return &aiboxQueryService{documents: session, temporalRepo: repo, adapterURL: strings.TrimSpace(os.Getenv(temporalAdapterURLEnv))}
}

func parseAIBOXTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		u := t.UTC()
		return &u, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		u := t.UTC()
		return &u, nil
	}
	return nil, fmt.Errorf("time must be RFC3339 or YYYY-MM-DD")
}

func inferredAIBOXDate(query string) *time.Time {
	match := aiboxDatePattern.FindStringSubmatch(query)
	if len(match) == 0 {
		return nil
	}
	raw := match[1]
	if match[2] != "" {
		raw += "T" + match[2]
	}
	if t, err := parseAIBOXTime(raw); err == nil {
		return t
	}
	return nil
}

func planAIBOXQuery(input types.AIBOXQueryInput) (types.AIBOXQueryMode, bool, *time.Time, error) {
	mode := types.AIBOXQueryMode(strings.ToLower(strings.TrimSpace(string(input.Mode))))
	if mode == "" {
		mode = types.AIBOXQueryModeAuto
	}
	validTime, err := parseAIBOXTime(input.ValidTime)
	if err != nil {
		return "", false, nil, err
	}
	knownTime, err := parseAIBOXTime(input.KnownTime)
	if err != nil {
		return "", false, nil, err
	}
	switch mode {
	case types.AIBOXQueryModeCurrent:
		now := time.Now().UTC()
		return mode, true, &now, nil
	case types.AIBOXQueryModeHistory:
		return mode, true, nil, nil
	case types.AIBOXQueryModeValidAt:
		if validTime == nil {
			return "", true, nil, fmt.Errorf("valid_time is required for valid_at mode")
		}
		return mode, true, validTime, nil
	case types.AIBOXQueryModeKnownAt:
		if knownTime == nil {
			return "", true, nil, fmt.Errorf("known_time is required for known_at mode")
		}
		return mode, true, knownTime, nil
	case types.AIBOXQueryModeAuto:
	default:
		return "", false, nil, fmt.Errorf("unsupported AIBOX query mode %q", mode)
	}
	q := strings.ToLower(input.Query)
	date := inferredAIBOXDate(input.Query)
	knownWords := []string{"known at", "what did we know", "as known", "当时知道", "截至当时", "当时我们知道"}
	for _, w := range knownWords {
		if strings.Contains(q, w) {
			if date == nil {
				return types.AIBOXQueryModeKnownAt, true, nil, fmt.Errorf("known_at intent requires a parseable date")
			}
			return types.AIBOXQueryModeKnownAt, true, date, nil
		}
	}
	asOfWords := []string{"as of", "at that time", "on that date", "截至", "当时", "那时", "在那天"}
	for _, w := range asOfWords {
		if strings.Contains(q, w) {
			if date == nil {
				return types.AIBOXQueryModeValidAt, true, nil, fmt.Errorf("valid_at intent requires a parseable date")
			}
			return types.AIBOXQueryModeValidAt, true, date, nil
		}
	}
	historyWords := []string{"history", "historical", "timeline", "evolution", "历史", "时间线", "演变", "变化过程"}
	for _, w := range historyWords {
		if strings.Contains(q, w) {
			return types.AIBOXQueryModeHistory, true, nil, nil
		}
	}
	currentWords := []string{"current", "currently", "right now", "status", "latest", "现在", "当前", "目前", "现状", "状态", "最新"}
	for _, w := range currentWords {
		if strings.Contains(q, w) {
			now := time.Now().UTC()
			return types.AIBOXQueryModeCurrent, true, &now, nil
		}
	}
	return types.AIBOXQueryModeAuto, false, nil, nil
}

func (s *aiboxQueryService) Query(ctx context.Context, input types.AIBOXQueryInput) (*types.AIBOXQueryResult, error) {
	result := &types.AIBOXQueryResult{EvaluatedAt: time.Now().UTC()}
	mode, temporalRequired, effectiveTime, planErr := planAIBOXQuery(input)
	result.EffectiveMode, result.TemporalRequired, result.EffectiveTime = mode, temporalRequired, effectiveTime
	if planErr != nil {
		result.TemporalDegraded = temporalRequired
		result.DegradedReasons = append(result.DegradedReasons, planErr.Error())
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultAIBOXQueryLimit
	}
	if limit > 50 {
		limit = 50
	}
	if s.documents != nil && strings.TrimSpace(input.Query) != "" && len(input.KBIDs) > 0 {
		docs, err := s.documents.SearchKnowledge(ctx, input.KBIDs, nil, nil, input.Query, &types.KnowledgeSearchOptions{MatchCount: min(limit, 20)})
		if err != nil {
			result.DocumentDegraded = true
			result.DegradedReasons = append(result.DegradedReasons, "document retrieval: "+err.Error())
		} else if docs != nil {
			for _, hit := range docs.Results {
				if hit == nil {
					continue
				}
				result.Documents = append(result.Documents, types.AIBOXDocumentEvidence{ChunkID: hit.ID, KnowledgeID: hit.KnowledgeID, KnowledgeTitle: hit.KnowledgeTitle, Content: hit.Content, Score: hit.Score})
			}
		}
	}
	if !temporalRequired || planErr != nil {
		return result, nil
	}
	if len(input.KBIDs) == 0 {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, "temporal query has no concrete knowledge-base scope")
		return result, nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("workspace ID not found in context")
	}
	if s.temporalRepo == nil || s.adapterURL == "" {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, "temporal adapter is not configured")
		return result, nil
	}
	var groups, generations []string
	for _, kbID := range input.KBIDs {
		generation, err := s.temporalRepo.GetActiveGeneration(ctx, tenantID, kbID)
		if err != nil {
			return nil, fmt.Errorf("resolve temporal generation for %s: %w", kbID, err)
		}
		if generation == nil {
			result.TemporalDegraded = true
			result.DegradedReasons = append(result.DegradedReasons, "no active temporal generation for knowledge base "+kbID)
			continue
		}
		groups = append(groups, generation.GroupID)
		generations = append(generations, generation.GenerationID)
	}
	if result.TemporalDegraded || len(groups) == 0 {
		return result, nil
	}
	incomplete, err := s.temporalRepo.HasIncompleteEpisodeBindings(ctx, generations)
	if err != nil {
		return nil, fmt.Errorf("check temporal binding freshness: %w", err)
	}
	if incomplete {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, "active temporal generation has incomplete or failed episode bindings")
		return result, nil
	}
	client, err := NewTemporalFactService(s.adapterURL, nil)
	if err != nil {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, err.Error())
		return result, nil
	}
	searchReq := TemporalSearchRequest{Query: input.Query, GroupIDs: groups, Mode: string(mode), Limit: limit}
	if mode == types.AIBOXQueryModeValidAt {
		searchReq.ValidTime = effectiveTime
	}
	if mode == types.AIBOXQueryModeKnownAt {
		searchReq.KnownTime = effectiveTime
	}
	search, err := client.Search(ctx, searchReq)
	if err != nil {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, "temporal retrieval: "+err.Error())
		return result, nil
	}
	if search.Freshness.Degraded {
		result.TemporalDegraded = true
		result.DegradedReasons = append(result.DegradedReasons, "temporal adapter reported degraded freshness")
	}
	var episodeUUIDs []string
	for _, fact := range search.Facts {
		episodeUUIDs = append(episodeUUIDs, fact.Episodes...)
	}
	bindings, err := s.temporalRepo.ListEpisodeBindingsByUUIDs(ctx, generations, aiboxUniqueStrings(episodeUUIDs), mode == types.AIBOXQueryModeCurrent)
	if err != nil {
		return nil, fmt.Errorf("load temporal support bindings: %w", err)
	}
	bindingByEpisode := make(map[string]types.TemporalEpisodeBinding, len(bindings))
	for _, binding := range bindings {
		bindingByEpisode[binding.EpisodeUUID] = binding
	}
	for _, fact := range search.Facts {
		authorities := map[string]struct{}{}
		supported := mode != types.AIBOXQueryModeCurrent
		for _, episode := range fact.Episodes {
			if b, exists := bindingByEpisode[episode]; exists {
				supported = true
				if b.AuthorityKey != "" {
					authorities[b.AuthorityKey] = struct{}{}
				}
			}
		}
		if !supported {
			continue
		}
		result.TemporalFacts = append(result.TemporalFacts, toAIBOXTemporalEvidence(fact, aiboxSortedKeys(authorities)))
	}
	result.Conflicts = detectAIBOXConflicts(result.TemporalFacts)
	return result, nil
}

func aiboxUniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
func aiboxSortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func toAIBOXTemporalEntity(v TemporalSearchEntity) types.AIBOXTemporalEntity {
	return types.AIBOXTemporalEntity{UUID: v.UUID, Name: v.Name, Labels: v.Labels, Attributes: v.Attributes}
}
func toAIBOXTemporalEvidence(f TemporalSearchFact, authorities []string) types.AIBOXTemporalEvidence {
	return types.AIBOXTemporalEvidence{FactUUID: f.UUID, Fact: f.Fact, Relation: f.Relation, GroupID: f.GroupID, Episodes: f.Episodes, AuthorityKeys: authorities, CreatedAt: f.CreatedAt, ValidAt: f.ValidAt, InvalidAt: f.InvalidAt, ExpiredAt: f.ExpiredAt, ReferenceTime: f.ReferenceTime, Attributes: f.Attributes, Source: toAIBOXTemporalEntity(f.Source), Target: toAIBOXTemporalEntity(f.Target)}
}
func detectAIBOXConflicts(facts []types.AIBOXTemporalEvidence) []types.AIBOXConflict {
	groups := map[string][]types.AIBOXTemporalEvidence{}
	for _, f := range facts {
		key := f.GroupID + "|" + f.Source.UUID + "|" + f.Relation
		groups[key] = append(groups[key], f)
	}
	var out []types.AIBOXConflict
	for key, claims := range groups {
		if len(claims) < 2 {
			continue
		}
		texts := map[string]struct{}{}
		auth := map[string]struct{}{}
		for _, c := range claims {
			texts[strings.TrimSpace(strings.ToLower(c.Fact))] = struct{}{}
			for _, a := range c.AuthorityKeys {
				auth[a] = struct{}{}
			}
		}
		if len(texts) > 1 && len(auth) > 1 {
			out = append(out, types.AIBOXConflict{Key: key, Claims: claims})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
