package memory

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maomeng/aim/pkg/logx"
)

// IDGenerator generates unique IDs for episodes and facts.
type IDGenerator interface {
	Generate() (int64, error)
}

// ProfileChatFunc sends a prompt to an LLM and returns the generated text.
// The caller binds the model selection logic (modelID, modelName, ownerID).
type ProfileChatFunc func(ctx context.Context, modelID int64, modelName string, ownerID int64, prompt string) (string, error)

// Manager is the memory system facade.
type Manager struct {
	logger      logx.Logger
	store       Store
	extractor   *Extractor
	idGen       IDGenerator
	embedder    Embedder
	vector      *MemoryVectorStore
	vectorTopK  int
	profileChat ProfileChatFunc
}

// NewManager creates a new memory Manager.
func NewManager(logger logx.Logger, store Store, extractor *Extractor, idGen IDGenerator, embedder Embedder, vector *MemoryVectorStore) *Manager {
	m := &Manager{
		logger:     logger,
		store:      store,
		extractor:  extractor,
		idGen:      idGen,
		embedder:   embedder,
		vector:     vector,
		vectorTopK: 3,
	}
	return m
}

// SetVectorTopK sets the vector oversample multiplier.
func (m *Manager) SetVectorTopK(mult int) {
	if mult > 0 {
		m.vectorTopK = mult
	}
}

// SetProfileChat sets the LLM function used for profile generation.
func (m *Manager) SetProfileChat(fn ProfileChatFunc) {
	if m != nil {
		m.profileChat = fn
	}
}

// WithExtractor returns a manager view using the same store, embedder, vector, and ID generator.
func (m *Manager) WithExtractor(extractor *Extractor) *Manager {
	if m == nil {
		return nil
	}
	return &Manager{
		logger:     m.logger,
		store:      m.store,
		extractor:  extractor,
		idGen:      m.idGen,
		embedder:   m.embedder,
		vector:     m.vector,
		vectorTopK: m.vectorTopK,
	}
}

// RememberAsync extracts memories from a user message and stores them asynchronously.
func (m *Manager) RememberAsync(extractCtx context.Context, input ExtractInput) {
	if m == nil || m.store == nil || m.extractor == nil || input.Message == "" {
		return
	}
	go func() {
		extractCtx := context.WithoutCancel(extractCtx)
		extractCtx, cancel := context.WithTimeout(extractCtx, 120*time.Second)
		defer cancel()

		if input.SentAt.IsZero() {
			input.SentAt = time.Now()
		}
		episodeID, err := m.nextID()
		if err != nil {
			m.withLogger(extractCtx).Errorf("memory episode id failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
			return
		}
		episode := &Episode{
			ID:        episodeID,
			BotID:     input.BotID,
			UserID:    input.UserID,
			ConvID:    input.ConvID,
			MsgID:     input.MsgID,
			Actor:     input.Username,
			Content:   input.Message,
			CreatedAt: input.SentAt,
		}
		if err := m.store.SaveEpisode(extractCtx, episode); err != nil {
			m.withLogger(extractCtx).Errorf("memory episode save failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
		}

		result, err := m.extractor.Extract(extractCtx, input)
		if err != nil {
			m.withLogger(extractCtx).Errorf("memory extraction failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
			return
		}
		if len(result.Facts) == 0 {
			return
		}

		facts := make([]Fact, 0, len(result.Facts))
		now := time.Now()
		for _, extracted := range result.Facts {
			factID, err := m.nextID()
			if err != nil {
				m.withLogger(extractCtx).Errorf("memory fact id failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
				continue
			}
			validAt, invalidAt := resolveTemporal(input.SentAt, extracted.TemporalHint)
			fact := Fact{
				ID:           factID,
				BotID:        input.BotID,
				UserID:       input.UserID,
				ConvID:       input.ConvID,
				MsgID:        input.MsgID,
				Subject:      extracted.Subject,
				Predicate:    extracted.Predicate,
				Object:       extracted.Object,
				EntityType:   extracted.EntityType,
				Category:     extracted.Category,
				Content:      extracted.Content,
				Evidence:     extracted.Evidence,
				Importance:   extracted.Importance,
				Confidence:   extracted.Confidence,
				ValidAt:      validAt,
				InvalidAt:    invalidAt,
				CreatedAt:    now,
				TemporalHint: extracted.TemporalHint,
			}
			fact.SearchText = buildSearchText(fact)
			facts = append(facts, fact)
		}
		if len(facts) == 0 {
			return
		}
		if err := m.store.AddFacts(extractCtx, facts); err != nil {
			m.withLogger(extractCtx).Errorf("memory facts save failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
			return
		}

		// Optional: index facts in vector store.
		if m.embedder != nil && m.vector != nil {
			texts := make([]string, len(facts))
			for i, f := range facts {
				texts[i] = f.Content
			}
			vectors, vecErr := m.embedder.Embed(extractCtx, texts, input.EmbeddingModelID, input.OwnerID)
			if vecErr != nil {
				m.withLogger(extractCtx).Errorf("memory embed failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, vecErr)
				return
			}
			if err := m.vector.UpsertFacts(extractCtx, facts, vectors); err != nil {
				m.withLogger(extractCtx).Errorf("memory vector upsert failed: bot_id=%d user_id=%d error=%v", input.BotID, input.UserID, err)
			}
		}

		// Trigger profile refresh if enough incremental facts have accumulated.
		if m.profileChat != nil && input.MemoryModelID > 0 {
			if text, updatedAt, err := m.store.GetProfileData(extractCtx, input.BotID, input.UserID); err == nil && m.shouldRefresh(text, updatedAt, input.BotID, input.UserID) {
				bgCtx := context.WithoutCancel(extractCtx)
				go m.GenerateProfile(bgCtx, input.BotID, input.UserID, input.MemoryModelID, input.MemoryModelName, input.OwnerID)
			}
		}
	}()
}

// Search returns relevant memories (vector direct facts + entity graph traversal with rank-based fusion).
func (m *Manager) Search(ctx context.Context, query MemoryQuery) ([]Memory, error) {
	if query.Limit <= 0 {
		query.Limit = 5
	}
	if m == nil || m.store == nil {
		return nil, nil
	}

	scope := Scope{BotID: query.BotID, UserID: query.UserID}
	historical := isHistoricalQuery(query.Query)
	var directItems []Memory
	scoreMap := make(map[int64]float64)

	// Step 1: hybrid vector search for direct facts.
	if m.embedder != nil && m.vector != nil && query.EmbeddingModelID > 0 {
		vecs, err := m.embedder.Embed(ctx, []string{query.Query}, query.EmbeddingModelID, query.OwnerID)
		if err != nil {
			m.withLogger(ctx).Errorf("memory query embed failed: error=%v", err)
		} else if len(vecs) > 0 && len(vecs[0]) > 0 {
			candidateLimit := query.Limit * m.vectorTopK
			hits, vecErr := m.vector.HybridSearch(ctx, vecs[0], query.Query, candidateLimit, MemoryVectorFilter{
				BotID: query.BotID, UserID: query.UserID,
			})
			if vecErr != nil {
				m.withLogger(ctx).Errorf("memory vector search failed: error=%v", vecErr)
			} else if len(hits) > 0 {
				ids := make([]int64, len(hits))
				for i, h := range hits {
					ids[i] = h.FactID
					scoreMap[h.FactID] = h.Score
				}
				items, idErr := m.store.SearchByIDs(ctx, scope, ids, historical)
				if idErr != nil {
					m.withLogger(ctx).Errorf("memory search by ids failed: error=%v", idErr)
				} else {
					rerank(items, scoreMap)
					directItems = items
				}
			}
		}
	}

	// Step 2: always let graph traversal contribute at least a small candidate set.
	traversalLimit := graphTraversalLimit(query.Limit, len(directItems))
	traversalItems, travErr := m.store.SearchWithTraversal(ctx, scope, query.Query, traversalLimit, 0)
	if travErr != nil {
		m.withLogger(ctx).Errorf("memory traversal search failed: error=%v", travErr)
	}

	// Step 3: unify direct and graph candidates with rank-based fusion.
	merged := fuseMemoryResults(directItems, scoreMap, traversalItems, query.Limit)
	fallbackUsed := false
	if len(merged) == 0 {
		fallbackUsed = true
		fallbackItems, err := m.store.Search(ctx, scope, query.Query, query.Limit)
		if err != nil {
			return nil, err
		}
		for i := range fallbackItems {
			fallbackItems[i].Source = "fallback"
			fallbackItems[i].Rank = i + 1
			fallbackItems[i].RankScore = 1 / float64(i+1)
			fallbackItems[i].FinalScore = 0.75*fallbackItems[i].RankScore + 0.25*memoryQuality(fallbackItems[i])
		}
		merged = fallbackItems
	}

	m.logSearchResults(ctx, query, len(directItems), len(traversalItems), fallbackUsed, merged)
	return merged, nil
}

// GetProfile returns the cached profile text. O(1) Neo4j read, no LLM call.
func (m *Manager) GetProfile(ctx context.Context, botID, userID int64) string {
	if m == nil || m.store == nil {
		return ""
	}
	text, _, err := m.store.GetProfileData(ctx, botID, userID)
	if err != nil || text == "" {
		return ""
	}
	return text
}

// GenerateProfile generates or refreshes the user profile from incremental facts.
// Called asynchronously; requires a profileChat function to be set.
func (m *Manager) GenerateProfile(ctx context.Context, botID, userID int64, modelID int64, modelName string, ownerID int64) {
	if m == nil || m.store == nil || m.profileChat == nil || modelID <= 0 {
		return
	}

	text, updatedAt, err := m.store.GetProfileData(ctx, botID, userID)
	if err != nil {
		m.withLogger(ctx).Errorf("profile get failed: bot_id=%d user_id=%d error=%v", botID, userID, err)
		return
	}

	var prompt string
	if text == "" {
		facts, fErr := m.store.GetInitialProfileFacts(ctx, botID, userID, 20)
		if fErr != nil || len(facts) == 0 {
			return
		}
		prompt = buildInitialProfilePrompt(facts)
	} else {
		newFacts, expiredFacts, fErr := m.store.GetIncrementalFacts(ctx, botID, userID, updatedAt)
		if fErr != nil || (len(newFacts) == 0 && len(expiredFacts) == 0) {
			return
		}
		prompt = buildIncrementalProfilePrompt(text, newFacts, expiredFacts)
	}

	resp, chatErr := m.profileChat(ctx, modelID, modelName, ownerID, prompt)
	if chatErr != nil {
		m.withLogger(ctx).Errorf("profile chat failed: bot_id=%d user_id=%d error=%v", botID, userID, chatErr)
		return
	}
	if resp == "" {
		return
	}

	if err := m.store.UpdateProfile(ctx, botID, userID, resp, time.Now()); err != nil {
		m.withLogger(ctx).Errorf("profile update failed: bot_id=%d user_id=%d error=%v", botID, userID, err)
	}
}

func (m *Manager) nextID() (int64, error) {
	if m.idGen == nil {
		return time.Now().UnixNano(), nil
	}
	return m.idGen.Generate()
}

func (m *Manager) withLogger(ctx context.Context) logx.Logger {
	if m.logger != nil {
		return m.logger.WithContext(ctx)
	}
	return logx.DefaultLogger().WithContext(ctx)
}

// rerank reorders items by vector score (primary), with fact quality as tiebreaker.
func rerank(items []Memory, scoreMap map[int64]float64) {
	sort.SliceStable(items, func(i, j int) bool {
		si := scoreMap[items[i].ID]
		sj := scoreMap[items[j].ID]
		if si != sj {
			return si > sj
		}
		qi := memoryQuality(items[i])
		qj := memoryQuality(items[j])
		return qi > qj
	})
}

func graphTraversalLimit(limit, directCount int) int {
	if limit <= 1 {
		return 1
	}
	if directCount == 0 {
		return limit
	}
	candidateLimit := limit / 2
	if candidateLimit < 1 {
		candidateLimit = 1
	}
	return candidateLimit
}

func fuseMemoryResults(directItems []Memory, directScores map[int64]float64, graphItems []Memory, limit int) []Memory {
	if limit <= 0 {
		limit = 5
	}
	best := make(map[int64]Memory, len(directItems)+len(graphItems))
	for i, item := range directItems {
		item.Source = "direct"
		item.SourceScore = directScores[item.ID]
		item.Hops = 0
		item.Rank = i + 1
		item.RankScore = 1 / float64(i+1)
		item.FinalScore = fusedMemoryScore(item)
		keepBestMemory(best, item)
	}
	for i, item := range graphItems {
		item.Source = "graph"
		item.Rank = i + 1
		baseRankScore := 1 / float64(i+1)
		if item.Hops > 0 {
			baseRankScore *= math.Pow(0.7, float64(item.Hops))
		}
		item.RankScore = baseRankScore
		item.FinalScore = fusedMemoryScore(item)
		keepBestMemory(best, item)
	}
	out := make([]Memory, 0, len(best))
	for _, item := range best {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FinalScore != out[j].FinalScore {
			return out[i].FinalScore > out[j].FinalScore
		}
		qi := memoryQuality(out[i])
		qj := memoryQuality(out[j])
		if qi != qj {
			return qi > qj
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func keepBestMemory(best map[int64]Memory, item Memory) {
	if item.ID == 0 {
		return
	}
	if existing, ok := best[item.ID]; !ok || item.FinalScore > existing.FinalScore {
		best[item.ID] = item
	}
}

func fusedMemoryScore(item Memory) float64 {
	return 0.75*clamp01(item.RankScore) + 0.25*memoryQuality(item)
}

func memoryQuality(item Memory) float64 {
	return clamp01(item.Importance * item.Confidence)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (m *Manager) logSearchResults(ctx context.Context, query MemoryQuery, directCount, graphCount int, fallbackUsed bool, results []Memory) {
	if m == nil {
		return
	}
	parts := make([]string, 0, min(len(results), 5))
	for i, item := range results {
		if i >= 5 {
			break
		}
		parts = append(parts, fmt.Sprintf("{id:%d source:%s rank:%d source_score:%.4f hops:%d rank_score:%.4f final:%.4f quality:%.4f content:%q}",
			item.ID, item.Source, item.Rank, item.SourceScore, item.Hops, item.RankScore, item.FinalScore, memoryQuality(item), previewMemoryContent(item.Content, 80)))
	}
	m.withLogger(ctx).Infof("memory search finished: bot_id=%d user_id=%d limit=%d query=%q direct_count=%d graph_count=%d fallback_used=%t result_count=%d results=[%s]",
		query.BotID, query.UserID, query.Limit, previewMemoryContent(query.Query, 120), directCount, graphCount, fallbackUsed, len(results), strings.Join(parts, ", "))
}

func previewMemoryContent(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (m *Manager) shouldRefresh(profileText string, updatedAt time.Time, botID, userID int64) bool {
	if profileText == "" {
		return true
	}
	if time.Since(updatedAt) < 5*time.Minute {
		return false
	}
	count, err := m.store.CountNewFactsSince(context.Background(), botID, userID, updatedAt)
	if err != nil {
		return false
	}
	return count >= 3
}

func buildInitialProfilePrompt(facts []ProfileFact) string {
	var sb string
	sb = "Distill a concise user profile (max ~150 words) from the facts below.\n\n"
	sb += "The profile captures only this user's most defining, stable characteristics — the kind of things you'd want to know to understand who they are in a conversation. Examples:\n"
	sb += "- Personality, temperament, working style, values\n- Long-term identity & background (career direction, education, home base)\n- Core skills and durable preferences\n\n"
	sb += "Rules:\n"
	sb += "- Write only the few traits that best define this user. Prefer omitting over listing.\n"
	sb += "- Ignore one-off, volatile, or trivial facts (what they recently watched/ate, places visited, temporary plans, single episodes) — such details live in the memory store and are retrieved on demand, they do not belong in the profile.\n"
	sb += "- Output as one natural paragraph, no bullet lists, no preamble, no explanation.\n"
	sb += "- Write the profile in the SAME language as the facts (the user's own language).\n\n"
	sb += "Facts (sorted by importance; bracket is category, use it to judge stability):\n"
	for _, f := range facts {
		sb += "- [" + f.Category + "] " + f.Content + "\n"
	}
	sb += "\nOutput the profile text directly."
	return sb
}

func buildIncrementalProfilePrompt(currentText string, newFacts, expiredFacts []ProfileFact) string {
	var sb string
	sb = "Current version of the user profile:\n"
	sb += currentText + "\n\n"

	if len(newFacts) > 0 {
		sb += "New facts since last update (bracket is category):\n"
		for _, f := range newFacts {
			sb += "- [" + f.Category + "] " + f.Content + "\n"
		}
		sb += "\n"
	}

	if len(expiredFacts) > 0 {
		sb += "Facts that have expired:\n"
		for _, f := range expiredFacts {
			sb += "- [" + f.Category + "] " + f.Content + "\n"
		}
		sb += "\n"
	}

	sb += "Update the profile based on the above. Rules:\n"
	sb += "- Keep only the user's most defining, stable traits (personality, values, long-term identity/background, core skills, durable preferences).\n"
	sb += "- Incorporate a new fact only if it reflects a stable change in such core traits; ignore one-off/volatile/trivial new facts (those stay in the memory store, retrieved on demand).\n"
	sb += "- For expired facts: if they touched a core trait in the profile, remove or update accordingly; otherwise ignore.\n"
	sb += "- Prefer omitting over stacking. Keep it under ~150 words.\n"
	sb += "- Write in the SAME language as the facts (the user's own language).\n"
	sb += "Output the updated full profile text directly, no preamble or explanation."
	return sb
}
