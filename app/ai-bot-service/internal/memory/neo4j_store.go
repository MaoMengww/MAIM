package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/maomeng/aim/pkg/logx"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// bfsMaxDepth is the maximum graph traversal depth for memory search.
// Matches Zep's default. Distance decay naturally suppresses results
// beyond 2 hops, so a generous limit (3) is safe.
const bfsMaxDepth = 3

// entityTraversalRelTypes are Entity→Entity relationship labels allowed during BFS.
// HAS_FACT is intentionally excluded so traversal cannot fan out through UserProfile.
const entityTraversalRelTypes = ":WORKS_AT|STUDIES_AT|LIVES_IN|PARTNER_OF|HAS_ROLE|MAJORS_IN|LIKES|DISLIKES|HATES|GOOD_AT|LEARNING|USES|KNOWS|COLLABORATES|FATHER_OF|MOTHER_OF|SIBLING_OF|WATCHED|READ|PLAYED|LISTENS_TO|INTERESTED_IN|VISITED|ATTENDED|HAS_PET"

// Neo4jStore implements temporal memory over Neo4j.
type Neo4jStore struct {
	driver neo4j.DriverWithContext
	dbName string
	logger logx.Logger
}

// NewNeo4jStore creates a Neo4j-backed memory store.
func NewNeo4jStore(driver neo4j.DriverWithContext, dbName string, logger logx.Logger) *Neo4jStore {
	if dbName == "" {
		dbName = "neo4j"
	}
	return &Neo4jStore{driver: driver, dbName: dbName, logger: logger}
}

// InitSchema creates constraints and indexes used by memory storage.
func (s *Neo4jStore) InitSchema(ctx context.Context) error {
	queries := []string{
		`CREATE CONSTRAINT memory_user_scope IF NOT EXISTS FOR (u:UserProfile) REQUIRE (u.botID, u.userID) IS UNIQUE`,
		`CREATE CONSTRAINT memory_episode_id IF NOT EXISTS FOR (e:Episode) REQUIRE e.id IS UNIQUE`,
		`CREATE CONSTRAINT memory_entity_scope IF NOT EXISTS FOR (e:Entity) REQUIRE (e.botID, e.userID, e.normalizedName, e.entityType) IS UNIQUE`,
		`CREATE FULLTEXT INDEX memory_entity_fulltext IF NOT EXISTS FOR (e:Entity) ON EACH [e.name, e.normalizedName, e.searchText]`,
		`CREATE FULLTEXT INDEX memory_episode_fulltext IF NOT EXISTS FOR (e:Episode) ON EACH [e.content]`,
	}
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.dbName})
	defer session.Close(ctx)
	for _, query := range queries {
		if _, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
			_, err := tx.Run(ctx, query, nil)
			return nil, err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Neo4jStore) SaveEpisode(ctx context.Context, episode *Episode) error {
	if episode == nil || episode.ID == 0 {
		return nil
	}
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
	MERGE (ep:Episode {id: $id})
	SET ep.botID = $botID,
	    ep.userID = $userID,
	    ep.convID = $convID,
	    ep.msgID = $msgID,
	    ep.actor = $actor,
	    ep.content = $content,
	    ep.createdAt = datetime($createdAt)
	`, map[string]any{
			"id":        episode.ID,
			"botID":     episode.BotID,
			"userID":    episode.UserID,
			"convID":    episode.ConvID,
			"msgID":     episode.MsgID,
			"actor":     episode.Actor,
			"content":   episode.Content,
			"createdAt": episode.CreatedAt.Format(time.RFC3339Nano),
		})
		return nil, err
	})
	return err
}

func (s *Neo4jStore) AddFacts(ctx context.Context, facts []Fact) error {
	if len(facts) == 0 {
		return nil
	}
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		for _, fact := range facts {
			if err := s.addFact(ctx, tx, fact); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (s *Neo4jStore) addFact(ctx context.Context, tx neo4j.ManagedTransaction, fact Fact) error {
	if fact.ID == 0 || fact.BotID == 0 || fact.UserID == 0 || fact.Object == "" || fact.Predicate == "" {
		return nil
	}
	now := time.Now()
	if fact.CreatedAt.IsZero() {
		fact.CreatedAt = now
	}
	if fact.ValidAt.IsZero() {
		fact.ValidAt = fact.CreatedAt
	}
	if fact.EntityType == "" {
		fact.EntityType = "unknown"
	}
	fact.Predicate = normalizePredicate(fact.Predicate)
	normalizedName := normalizeEntityName(fact.Object)
	if fact.SearchText == "" {
		fact.SearchText = buildSearchText(fact)
	}

	// --- Invalidate old HAS_FACT edges in the same exclusive group ---
	group := exclusiveGroup(fact.Predicate)
	if group != "" {
		_, err := tx.Run(ctx, `
	MATCH (:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(e:Entity)
	WHERE r.expiredAt IS NULL
	  AND r.invalidAt IS NULL
	  AND r.predicate IN $predicates
	  AND e.normalizedName <> $normalizedName
	SET r.invalidAt = datetime($validAt),
	    r.expiredAt = datetime($now)
	`, map[string]any{
			"botID":          fact.BotID,
			"userID":         fact.UserID,
			"predicates":     predicatesInGroup(group),
			"normalizedName": normalizedName,
			"validAt":        fact.ValidAt.Format(time.RFC3339Nano),
			"now":            now.Format(time.RFC3339Nano),
		})
		if err != nil {
			return err
		}
	}

	// --- Create HAS_FACT edge (User→Entity) ---
	_, err := tx.Run(ctx, `
	MERGE (u:UserProfile {botID: $botID, userID: $userID})
	ON CREATE SET u.createdAt = datetime($createdAt)
	SET u.updatedAt = datetime($createdAt)
	MERGE (e:Entity {botID: $botID, userID: $userID, normalizedName: $normalizedName, entityType: $entityType})
	ON CREATE SET e.firstSeen = datetime($createdAt)
	SET e.name = $object,
	    e.searchText = $searchText,
	    e.lastSeen = datetime($createdAt)
	WITH u, e
	WHERE NOT EXISTS {
	  MATCH (u)-[existing:HAS_FACT]->(e)
	  WHERE existing.predicate = $predicate
	    AND existing.expiredAt IS NULL
	    AND existing.invalidAt IS NULL
	}
	CREATE (u)-[:HAS_FACT {
	    id: $id,
	    predicate: $predicate,
	    category: $category,
	    content: $content,
	    evidence: $evidence,
	    confidence: $confidence,
	    importance: $importance,
	    validAt: datetime($validAt),
	    invalidAt: $invalidAt,
	    createdAt: datetime($createdAt),
	    expiredAt: $expiredAt,
	    temporalHint: $temporalHint,
	    sourceConvID: $convID,
	    sourceMsgID: $msgID,
	    accessCount: 0
	}]->(e)
	`, map[string]any{
		"id":             fact.ID,
		"botID":          fact.BotID,
		"userID":         fact.UserID,
		"convID":         fact.ConvID,
		"msgID":          fact.MsgID,
		"predicate":      fact.Predicate,
		"category":       fact.Category,
		"content":        fact.Content,
		"evidence":       fact.Evidence,
		"confidence":     fact.Confidence,
		"importance":     fact.Importance,
		"validAt":        fact.ValidAt.Format(time.RFC3339Nano),
		"invalidAt":      neo4jTimeOrNil(fact.InvalidAt),
		"createdAt":      fact.CreatedAt.Format(time.RFC3339Nano),
		"expiredAt":      neo4jTimeOrNil(fact.ExpiredAt),
		"temporalHint":   fact.TemporalHint,
		"object":         fact.Object,
		"normalizedName": normalizedName,
		"entityType":     fact.EntityType,
		"searchText":     fact.SearchText,
	})
	if err != nil {
		return err
	}

	// --- Create Entity→Entity edge for predicates with an EdgeLabel ---
	cfg, hasEdge := GetPredicateConfig(fact.Predicate)
	if hasEdge && cfg.ObjectAsEntity && cfg.EdgeLabel != "" {
		objectType := inferEntityTypeFromPredicate(fact.Predicate)
		normalizedObject := normalizeEntityName(fact.Object)
		createdAtStr := fact.CreatedAt.Format(time.RFC3339Nano)
		validAtStr := fact.ValidAt.Format(time.RFC3339Nano)

		// Ensure target Entity node exists.
		if _, mErr := tx.Run(ctx, `
		MERGE (obj:Entity {botID: $botID, userID: $userID, normalizedName: $objName, entityType: $objType})
		ON CREATE SET obj.name = $object, obj.firstSeen = datetime($now), obj.lastSeen = datetime($now)
		SET obj.lastSeen = datetime($now)
		`, map[string]any{
			"botID":   fact.BotID,
			"userID":  fact.UserID,
			"objName": normalizedObject,
			"objType": objectType,
			"object":  fact.Object,
			"now":     createdAtStr,
		}); mErr != nil {
			return mErr
		}

		// Invalidate old exclusive Entity→Entity edges of the same type.
		if cfg.Exclusive {
			_, exErr := tx.Run(ctx, `
			MATCH (subj:Entity {botID: $botID, userID: $userID, normalizedName: $subjName, entityType: $subjType})
			      -[old:`+cfg.EdgeLabel+`]->(:Entity)
			WHERE old.validTo IS NULL
			SET old.validTo = datetime($validAt)
			`, map[string]any{
				"botID":    fact.BotID,
				"userID":   fact.UserID,
				"subjName": normalizeEntityName(fact.Subject),
				"subjType": fact.EntityType,
				"validAt":  validAtStr,
			})
			if exErr != nil {
				return exErr
			}
		}

		// Create the Entity→Entity edge.
		_, eeErr := tx.Run(ctx, `
		MATCH (subj:Entity {botID: $botID, userID: $userID, normalizedName: $subjName, entityType: $subjType})
		MATCH (obj:Entity {botID: $botID, userID: $userID, normalizedName: $objName, entityType: $objType})
		MERGE (subj)-[r:`+cfg.EdgeLabel+`]->(obj)
		SET r.fact = $content,
		    r.confidence = $confidence,
		    r.importance = $importance,
		    r.validFrom = datetime($validAt),
		    r.validTo = NULL,
		    r.sourceConvID = $convID,
		    r.sourceMsgID = $msgID
		`, map[string]any{
			"botID":      fact.BotID,
			"userID":     fact.UserID,
			"subjName":   normalizeEntityName(fact.Subject),
			"subjType":   fact.EntityType,
			"objName":    normalizedObject,
			"objType":    objectType,
			"content":    fact.Content,
			"confidence": fact.Confidence,
			"importance": fact.Importance,
			"validAt":    validAtStr,
			"convID":     fact.ConvID,
			"msgID":      fact.MsgID,
		})
		if eeErr != nil {
			return eeErr
		}
	}

	return nil
}

// findExactEntity looks up an Entity by (botID, userID, normalizedName, entityType).
func (s *Neo4jStore) findExactEntity(ctx context.Context, scope Scope, normalizedName, entityType string) (*Entity, error) {
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
		MATCH (e:Entity {botID: $botID, userID: $userID, normalizedName: $name, entityType: $type})
		RETURN e.name AS name, e.normalizedName AS normalizedName, e.entityType AS entityType,
		       coalesce(e.aliases, []) AS aliases, e.firstSeen AS firstSeen, e.lastSeen AS lastSeen
		`, map[string]any{
			"botID": scope.BotID, "userID": scope.UserID,
			"name": normalizedName, "type": entityType,
		})
		if err != nil {
			return nil, err
		}
		if !result.Next(ctx) {
			return nil, nil
		}
		return recordToEntity(result.Record(), scope.BotID, scope.UserID), result.Err()
	})
	if err != nil || value == nil {
		return nil, err
	}
	return value.(*Entity), nil
}

// findByAlias finds an Entity whose aliases contain the given name.
func (s *Neo4jStore) findByAlias(ctx context.Context, scope Scope, name string) (*Entity, error) {
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
		MATCH (e:Entity {botID: $botID, userID: $userID})
		WHERE $name IN e.aliases
		RETURN e.name AS name, e.normalizedName AS normalizedName, e.entityType AS entityType,
		       coalesce(e.aliases, []) AS aliases, e.firstSeen AS firstSeen, e.lastSeen AS lastSeen
		LIMIT 1
		`, map[string]any{"botID": scope.BotID, "userID": scope.UserID, "name": name})
		if err != nil {
			return nil, err
		}
		if !result.Next(ctx) {
			return nil, nil
		}
		return recordToEntity(result.Record(), scope.BotID, scope.UserID), result.Err()
	})
	if err != nil || value == nil {
		return nil, err
	}
	return value.(*Entity), nil
}

// addAlias appends an alias to an Entity's aliases list.
func (s *Neo4jStore) addAlias(ctx context.Context, scope Scope, normalizedName, entityType, alias string) error {
	normalizedAlias := normalizeEntityName(alias)
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
		MATCH (e:Entity {botID: $botID, userID: $userID, normalizedName: $name, entityType: $type})
		SET e.aliases = CASE
		    WHEN e.aliases IS NULL THEN [$alias]
		    WHEN $alias IN e.aliases THEN e.aliases
		    ELSE e.aliases + $alias
		END
		`, map[string]any{
			"botID": scope.BotID, "userID": scope.UserID,
			"name": normalizedName, "type": entityType,
			"alias": normalizedAlias,
		})
		return nil, err
	})
	return err
}

func recordToEntity(record *neo4j.Record, botID, userID int64) *Entity {
	aliases := getStringSlice(record, "aliases")
	return &Entity{
		BotID:          botID,
		UserID:         userID,
		Name:           getString(record, "name"),
		NormalizedName: getString(record, "normalizedName"),
		EntityType:     getString(record, "entityType"),
		Aliases:        aliases,
		FirstSeen:      getTime(record, "firstSeen"),
		LastSeen:       getTime(record, "lastSeen"),
	}
}

func (s *Neo4jStore) Search(ctx context.Context, scope Scope, query string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 5
	}
	historical := isHistoricalQuery(query)
	items, err := s.searchFulltext(ctx, scope, query, limit, historical)
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		return items, nil
	}
	return s.searchFallback(ctx, scope, limit, historical)
}

func (s *Neo4jStore) searchFulltext(ctx context.Context, scope Scope, query string, limit int, historical bool) ([]Memory, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	where := "r.invalidAt IS NULL AND r.expiredAt IS NULL"
	order := "score DESC, r.importance * r.confidence DESC, r.createdAt DESC"
	if historical {
		where = "true"
		order = "r.validAt DESC, score DESC"
	}
	cypher := fmt.Sprintf(`
	CALL db.index.fulltext.queryNodes('memory_entity_fulltext', $query) YIELD node, score
	MATCH (:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(node)
	WHERE %s
	SET r.accessCount = coalesce(r.accessCount, 0) + 1
	RETURN r.id AS id,
	       r.content AS content,
	       r.category AS category,
	       r.importance AS importance,
	       r.confidence AS confidence,
	       r.validAt AS validAt,
	       r.invalidAt AS invalidAt,
	       r.createdAt AS createdAt,
	       r.expiredAt AS expiredAt,
	       r.temporalHint AS temporalHint
	ORDER BY %s
	LIMIT $limit
	`, where, order)
	return s.readMemories(ctx, cypher, map[string]any{"botID": scope.BotID, "userID": scope.UserID, "query": query, "limit": limit})
}

func (s *Neo4jStore) searchFallback(ctx context.Context, scope Scope, limit int, historical bool) ([]Memory, error) {
	where := "r.invalidAt IS NULL AND r.expiredAt IS NULL"
	order := "r.importance * r.confidence DESC, r.createdAt DESC"
	if historical {
		where = "true"
		order = "r.validAt DESC, r.createdAt DESC"
	}
	cypher := fmt.Sprintf(`
	MATCH (:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE %s
	SET r.accessCount = coalesce(r.accessCount, 0) + 1
	RETURN r.id AS id,
	       r.content AS content,
	       r.category AS category,
	       r.importance AS importance,
	       r.confidence AS confidence,
	       r.validAt AS validAt,
	       r.invalidAt AS invalidAt,
	       r.createdAt AS createdAt,
	       r.expiredAt AS expiredAt,
	       r.temporalHint AS temporalHint
	ORDER BY %s
	LIMIT $limit
	`, where, order)
	return s.readMemories(ctx, cypher, map[string]any{"botID": scope.BotID, "userID": scope.UserID, "limit": limit})
}

func (s *Neo4jStore) readMemories(ctx context.Context, cypher string, params map[string]any) ([]Memory, error) {
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, cypher, params)
		if err != nil {
			return nil, err
		}
		items := make([]Memory, 0)
		for result.Next(ctx) {
			record := result.Record()
			items = append(items, Memory{
				ID:           getInt64(record, "id"),
				Content:      getString(record, "content"),
				Category:     getString(record, "category"),
				Importance:   getFloat64(record, "importance"),
				Confidence:   getFloat64(record, "confidence"),
				ValidAt:      getTime(record, "validAt"),
				InvalidAt:    getTimePtr(record, "invalidAt"),
				CreatedAt:    getTime(record, "createdAt"),
				ExpiredAt:    getTimePtr(record, "expiredAt"),
				TemporalHint: getString(record, "temporalHint"),
				Hops:         int(getInt64(record, "hops")),
				SourceScore:  getFloat64(record, "sourceScore"),
			})
		}
		return items, result.Err()
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return value.([]Memory), nil
}

// SearchByIDs loads memories by fact ids, applying temporal filters.
func (s *Neo4jStore) SearchByIDs(ctx context.Context, scope Scope, ids []int64, historical bool) ([]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	where := "r.invalidAt IS NULL AND r.expiredAt IS NULL"
	if historical {
		where = "true"
	}
	cypher := fmt.Sprintf(`
	MATCH (:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE r.id IN $ids AND %s
	SET r.accessCount = coalesce(r.accessCount, 0) + 1
	RETURN r.id AS id,
	       r.content AS content,
	       r.category AS category,
	       r.importance AS importance,
	       r.confidence AS confidence,
	       r.validAt AS validAt,
	       r.invalidAt AS invalidAt,
	       r.createdAt AS createdAt,
	       r.expiredAt AS expiredAt,
	       r.temporalHint AS temporalHint
	`, where)
	return s.readMemories(ctx, cypher, map[string]any{
		"botID":  scope.BotID,
		"userID": scope.UserID,
		"ids":    ids,
	})
}

// SearchWithTraversal performs multi-hop graph traversal from fulltext-matched entities.
// Uses bfsMaxDepth (3) and distance-decay scoring so distant results are naturally suppressed.
func (s *Neo4jStore) SearchWithTraversal(ctx context.Context, scope Scope, query string, limit int, maxHops int) ([]Memory, error) {
	if limit <= 0 {
		limit = 5
	}
	// Clamp to constant; maxHops is kept for interface compatibility.
	if maxHops <= 0 || maxHops > bfsMaxDepth {
		maxHops = bfsMaxDepth
	}

	historical := isHistoricalQuery(query)
	whereClause := "r.invalidAt IS NULL AND r.expiredAt IS NULL"
	if historical {
		whereClause = "true"
	}

	cypher := fmt.Sprintf(`
	// Step 1: fulltext search on Entity nodes
	CALL db.index.fulltext.queryNodes('memory_entity_fulltext', $query) YIELD node AS entry, score
	WHERE entry.botID = $botID AND entry.userID = $userID

	// Step 2: 0-hop — direct HAS_FACT on the matched entity
	OPTIONAL MATCH (u:UserProfile {botID: $botID, userID: $userID})
	              -[direct:HAS_FACT]->(entry)
	WHERE %s
	SET direct.accessCount = coalesce(direct.accessCount, 0) + 1
	WITH entry, score, u, collect({
	    id: direct.id, content: direct.content, category: direct.category,
	    importance: direct.importance, confidence: direct.confidence,
	    validAt: direct.validAt, invalidAt: direct.invalidAt,
	    createdAt: direct.createdAt, expiredAt: direct.expiredAt,
	    temporalHint: direct.temporalHint, hops: 0, weight: score
	}) AS directResults

	// Step 3: 1..N hop — traverse only explicit Entity→Entity edges.
	OPTIONAL MATCH path = (entry)-[%s*1..%d]-(connected:Entity)
	WHERE all(n IN nodes(path) WHERE n:Entity)
	  AND all(edge IN relationships(path)
	          WHERE edge.validTo IS NULL OR edge.validTo > datetime())

	// Step 4: HAS_FACT on the connected entities
	OPTIONAL MATCH (u)-[indirect:HAS_FACT]->(connected)
	WHERE %s
	SET indirect.accessCount = coalesce(indirect.accessCount, 0) + 1

	WITH directResults, collect({
	    id: indirect.id, content: indirect.content, category: indirect.category,
	    importance: indirect.importance, confidence: indirect.confidence,
	    validAt: indirect.validAt, invalidAt: indirect.invalidAt,
	    createdAt: indirect.createdAt, expiredAt: indirect.expiredAt,
	    temporalHint: indirect.temporalHint,
	    hops: length(path), weight: score * (1.0 / (1.0 + length(path)))
	}) AS indirectResults

	WITH directResults + indirectResults AS allResults
	UNWIND allResults AS result
	WITH result WHERE result.id IS NOT NULL
	RETURN result.id AS id,
	       result.content AS content,
	       result.category AS category,
	       result.importance AS importance,
	       result.confidence AS confidence,
	       result.validAt AS validAt,
	       result.invalidAt AS invalidAt,
	       result.createdAt AS createdAt,
	       result.expiredAt AS expiredAt,
	       result.temporalHint AS temporalHint,
	       result.hops AS hops,
	       result.weight AS sourceScore
	ORDER BY result.weight DESC, result.importance * result.confidence DESC
	LIMIT $limit
	`, whereClause, entityTraversalRelTypes, maxHops, whereClause)

	return s.readMemories(ctx, cypher, map[string]any{
		"botID":  scope.BotID,
		"userID": scope.UserID,
		"query":  query,
		"limit":  limit,
	})
}

// GetProfileData returns the current profile text and last update time for a user.
func (s *Neo4jStore) GetProfileData(ctx context.Context, botID, userID int64) (string, time.Time, error) {
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
	MATCH (u:UserProfile {botID: $botID, userID: $userID})
	RETURN coalesce(u.profileText, '') AS text, u.profileUpdatedAt AS updatedAt
	`, map[string]any{"botID": botID, "userID": userID})
		if err != nil {
			return nil, err
		}
		if !result.Next(ctx) {
			return []any{"", time.Time{}}, nil
		}
		record := result.Record()
		return []any{getString(record, "text"), getTime(record, "updatedAt")}, result.Err()
	})
	if err != nil {
		return "", time.Time{}, err
	}
	if value == nil {
		return "", time.Time{}, nil
	}
	pair := value.([]any)
	return pair[0].(string), pair[1].(time.Time), nil
}

// CountNewFactsSince returns how many active facts were created since the given time.
func (s *Neo4jStore) CountNewFactsSince(ctx context.Context, botID, userID int64, since time.Time) (int, error) {
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
	MATCH (u:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE r.createdAt > $since
	  AND r.invalidAt IS NULL AND r.expiredAt IS NULL
	RETURN count(r) AS cnt
	`, map[string]any{"botID": botID, "userID": userID, "since": since.Format(time.RFC3339Nano)})
		if err != nil {
			return nil, err
		}
		if !result.Next(ctx) {
			return int64(0), result.Err()
		}
		return result.Record().Values[0], result.Err()
	})
	if err != nil {
		return 0, err
	}
	if value == nil {
		return 0, nil
	}
	return int(value.(int64)), nil
}

// GetIncrementalFacts returns new active facts and recently-expired facts since the given time.
func (s *Neo4jStore) GetIncrementalFacts(ctx context.Context, botID, userID int64, since time.Time) ([]ProfileFact, []ProfileFact, error) {
	// New active facts.
	newValue, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
	MATCH (u:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE r.createdAt > $since
	  AND r.invalidAt IS NULL AND r.expiredAt IS NULL
	RETURN r.content AS content, r.category AS category, r.importance AS importance, r.temporalHint AS temporalHint
	ORDER BY r.createdAt DESC
	LIMIT 50
	`, map[string]any{"botID": botID, "userID": userID, "since": since.Format(time.RFC3339Nano)})
		if err != nil {
			return nil, err
		}
		var facts []ProfileFact
		for result.Next(ctx) {
			record := result.Record()
			facts = append(facts, ProfileFact{
				Content:      getString(record, "content"),
				Category:     getString(record, "category"),
				Importance:   getFloat64(record, "importance"),
				TemporalHint: getString(record, "temporalHint"),
			})
		}
		return facts, result.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	newFacts := newValue.([]ProfileFact)

	// Recently-expired facts.
	expValue, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
	MATCH (u:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE r.expiredAt > $since
	RETURN r.content AS content, r.category AS category, r.importance AS importance, r.temporalHint AS temporalHint
	ORDER BY r.expiredAt DESC
	`, map[string]any{"botID": botID, "userID": userID, "since": since.Format(time.RFC3339Nano)})
		if err != nil {
			return nil, err
		}
		var facts []ProfileFact
		for result.Next(ctx) {
			record := result.Record()
			facts = append(facts, ProfileFact{
				Content:      getString(record, "content"),
				Category:     getString(record, "category"),
				Importance:   getFloat64(record, "importance"),
				TemporalHint: getString(record, "temporalHint"),
			})
		}
		return facts, result.Err()
	})
	if err != nil {
		return nil, nil, err
	}
	expFacts := expValue.([]ProfileFact)

	return newFacts, expFacts, nil
}

// GetInitialProfileFacts returns top N active facts by importance for first-time profile generation.
func (s *Neo4jStore) GetInitialProfileFacts(ctx context.Context, botID, userID int64, limit int) ([]ProfileFact, error) {
	if limit <= 0 {
		limit = 20
	}
	value, err := s.read(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		result, err := tx.Run(ctx, `
	MATCH (u:UserProfile {botID: $botID, userID: $userID})-[r:HAS_FACT]->(:Entity)
	WHERE r.invalidAt IS NULL AND r.expiredAt IS NULL
	RETURN r.content AS content, r.category AS category, r.importance AS importance, r.temporalHint AS temporalHint
	ORDER BY r.importance DESC
	LIMIT $limit
	`, map[string]any{"botID": botID, "userID": userID, "limit": limit})
		if err != nil {
			return nil, err
		}
		var facts []ProfileFact
		for result.Next(ctx) {
			record := result.Record()
			facts = append(facts, ProfileFact{
				Content:      getString(record, "content"),
				Category:     getString(record, "category"),
				Importance:   getFloat64(record, "importance"),
				TemporalHint: getString(record, "temporalHint"),
			})
		}
		return facts, result.Err()
	})
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, nil
	}
	return value.([]ProfileFact), nil
}

// UpdateProfile sets the profile text and updatedAt on the UserProfile node.
func (s *Neo4jStore) UpdateProfile(ctx context.Context, botID, userID int64, profileText string, updatedAt time.Time) error {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.write(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		_, err := tx.Run(ctx, `
	MERGE (u:UserProfile {botID: $botID, userID: $userID})
	ON CREATE SET u.createdAt = datetime($now)
	SET u.profileText = $text,
	    u.profileUpdatedAt = datetime($now)
	`, map[string]any{"botID": botID, "userID": userID, "text": profileText, "now": updatedAt.Format(time.RFC3339Nano)})
		return nil, err
	})
	return err
}

func (s *Neo4jStore) read(ctx context.Context, work neo4j.ManagedTransactionWork) (any, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.dbName, AccessMode: neo4j.AccessModeRead})
	defer session.Close(ctx)
	return session.ExecuteRead(ctx, work)
}

func (s *Neo4jStore) write(ctx context.Context, work neo4j.ManagedTransactionWork) (any, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: s.dbName, AccessMode: neo4j.AccessModeWrite})
	defer session.Close(ctx)
	return session.ExecuteWrite(ctx, work)
}

func neo4jTimeOrNil(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339Nano)
}

func getInt64(record *neo4j.Record, key string) int64 {
	value, _ := record.Get(key)
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

func getString(record *neo4j.Record, key string) string {
	value, _ := record.Get(key)
	if v, ok := value.(string); ok {
		return v
	}
	return ""
}

func getStringSlice(record *neo4j.Record, key string) []string {
	value, _ := record.Get(key)
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func getFloat64(record *neo4j.Record, key string) float64 {
	value, _ := record.Get(key)
	switch v := value.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	default:
		return 0
	}
}

func getTime(record *neo4j.Record, key string) time.Time {
	value, _ := record.Get(key)
	if v, ok := value.(time.Time); ok {
		return v
	}
	return time.Time{}
}

func getTimePtr(record *neo4j.Record, key string) *time.Time {
	value, _ := record.Get(key)
	if v, ok := value.(time.Time); ok {
		return &v
	}
	return nil
}
