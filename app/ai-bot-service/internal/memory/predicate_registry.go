package memory

// PredicateConfig defines how a predicate maps to the graph.
// Predicates with an EdgeLabel create Entity→Entity relationships in addition to HAS_FACT.
type PredicateConfig struct {
	Predicate      string // normalized predicate name
	Category       string // fact category
	EdgeLabel      string // Neo4j relationship label, empty means no Entity edge
	ObjectAsEntity bool   // whether Object should be treated as an independent Entity node
	Exclusive      bool   // only one active edge of this type at a time (invalidates old edges)
}

// predicateRegistry maps normalized predicates to their graph behavior.
var predicateRegistry = map[string]PredicateConfig{
	// ═══════════════════════════════════════════════════════════════════
	// Exclusive relationships (only one can be active at a time)
	// ═══════════════════════════════════════════════════════════════════
	"works_at":       {Predicate: "works_at", Category: "occupation", EdgeLabel: "WORKS_AT", ObjectAsEntity: true, Exclusive: true},
	"studies_at":     {Predicate: "studies_at", Category: "education", EdgeLabel: "STUDIES_AT", ObjectAsEntity: true, Exclusive: true},
	"lives_in":       {Predicate: "lives_in", Category: "location", EdgeLabel: "LIVES_IN", ObjectAsEntity: true, Exclusive: true},
	"has_partner":    {Predicate: "has_partner", Category: "family", EdgeLabel: "PARTNER_OF", ObjectAsEntity: true, Exclusive: true},

	// ═══════════════════════════════════════════════════════════════════
	// Non-exclusive relationships (can have multiple active edges)
	// ═══════════════════════════════════════════════════════════════════

	// --- Occupation & role ---
	"has_role":          {Predicate: "has_role", Category: "occupation", EdgeLabel: "HAS_ROLE", ObjectAsEntity: true, Exclusive: false},

	// --- Education ---
	"majors_in":         {Predicate: "majors_in", Category: "education", EdgeLabel: "MAJORS_IN", ObjectAsEntity: true, Exclusive: false},

	// --- Preference ---
	"likes":             {Predicate: "likes", Category: "preference", EdgeLabel: "LIKES", ObjectAsEntity: true, Exclusive: false},
	"dislikes":          {Predicate: "dislikes", Category: "preference", EdgeLabel: "DISLIKES", ObjectAsEntity: true, Exclusive: false},
	"hates":             {Predicate: "hates", Category: "preference", EdgeLabel: "HATES", ObjectAsEntity: true, Exclusive: false},

	// --- Skill & tool ---
	"main_language":     {Predicate: "main_language", Category: "skill", EdgeLabel: "", ObjectAsEntity: false},
	"proficient_at":     {Predicate: "proficient_at", Category: "skill", EdgeLabel: "GOOD_AT", ObjectAsEntity: true, Exclusive: false},
	"learning":          {Predicate: "learning", Category: "skill", EdgeLabel: "LEARNING", ObjectAsEntity: true, Exclusive: false},
	"uses_tool":         {Predicate: "uses_tool", Category: "tool", EdgeLabel: "USES", ObjectAsEntity: true, Exclusive: false},

	// --- Social ---
	"friend_of":         {Predicate: "friend_of", Category: "social", EdgeLabel: "KNOWS", ObjectAsEntity: true, Exclusive: false},
	"collaborates_with": {Predicate: "collaborates_with", Category: "social", EdgeLabel: "COLLABORATES", ObjectAsEntity: true, Exclusive: false},

	// --- Family ---
	"has_father":        {Predicate: "has_father", Category: "family", EdgeLabel: "FATHER_OF", ObjectAsEntity: true, Exclusive: true},
	"has_mother":        {Predicate: "has_mother", Category: "family", EdgeLabel: "MOTHER_OF", ObjectAsEntity: true, Exclusive: true},
	"has_sibling":       {Predicate: "has_sibling", Category: "family", EdgeLabel: "SIBLING_OF", ObjectAsEntity: true, Exclusive: false},

	// --- Media & entertainment ---
	"watched":           {Predicate: "watched", Category: "media", EdgeLabel: "WATCHED", ObjectAsEntity: true, Exclusive: false},
	"read":              {Predicate: "read", Category: "media", EdgeLabel: "READ", ObjectAsEntity: true, Exclusive: false},
	"played":            {Predicate: "played", Category: "media", EdgeLabel: "PLAYED", ObjectAsEntity: true, Exclusive: false},
	"listens_to":        {Predicate: "listens_to", Category: "media", EdgeLabel: "LISTENS_TO", ObjectAsEntity: true, Exclusive: false},

	// --- Interest & goal ---
	"interested_in":     {Predicate: "interested_in", Category: "interest", EdgeLabel: "INTERESTED_IN", ObjectAsEntity: true, Exclusive: false},

	// --- Travel & event ---
	"visited":           {Predicate: "visited", Category: "travel", EdgeLabel: "VISITED", ObjectAsEntity: true, Exclusive: false},
	"attended":          {Predicate: "attended", Category: "event", EdgeLabel: "ATTENDED", ObjectAsEntity: true, Exclusive: false},

	// --- Lifestyle & health ---
	"has_pet":           {Predicate: "has_pet", Category: "lifestyle", EdgeLabel: "HAS_PET", ObjectAsEntity: true, Exclusive: false},

	// ═══════════════════════════════════════════════════════════════════
	// Non-Entity predicates (Object is a value/attribute, not an Entity node)
	// ═══════════════════════════════════════════════════════════════════
	"degree_is":         {Predicate: "degree_is", Category: "education", EdgeLabel: "", ObjectAsEntity: false},
	"age_is":            {Predicate: "age_is", Category: "background", EdgeLabel: "", ObjectAsEntity: false},
	"birthday_is":       {Predicate: "birthday_is", Category: "background", EdgeLabel: "", ObjectAsEntity: false},
	"personality_trait": {Predicate: "personality_trait", Category: "personality", EdgeLabel: "", ObjectAsEntity: false},
	"goal_is":           {Predicate: "goal_is", Category: "goal", EdgeLabel: "", ObjectAsEntity: false},
	"plans_to":          {Predicate: "plans_to", Category: "goal", EdgeLabel: "", ObjectAsEntity: false},
	"diet":              {Predicate: "diet", Category: "lifestyle", EdgeLabel: "", ObjectAsEntity: false},
	"exercise":          {Predicate: "exercise", Category: "lifestyle", EdgeLabel: "", ObjectAsEntity: false},
	"sleep_habit":       {Predicate: "sleep_habit", Category: "lifestyle", EdgeLabel: "", ObjectAsEntity: false},
}

// GetPredicateConfig returns the config for a predicate, or false if not registered.
func GetPredicateConfig(predicate string) (PredicateConfig, bool) {
	cfg, ok := predicateRegistry[normalizePredicate(predicate)]
	return cfg, ok
}

// inferEntityTypeFromPredicate deduces the Entity entityType from the predicate.
func inferEntityTypeFromPredicate(predicate string) string {
	switch normalizePredicate(predicate) {
	// Occupation & role
	case "works_at":
		return "organization"
	case "has_role":
		return "role"

	// Education
	case "studies_at":
		return "school"
	case "majors_in":
		return "major"

	// Location
	case "lives_in", "visited":
		return "location"

	// Preference
	case "likes", "dislikes", "hates":
		return "interest"

	// Skill
	case "proficient_at", "learning":
		return "skill"
	case "main_language":
		return "language"

	// Tool
	case "uses_tool", "main_tool":
		return "tool"

	// Social & family
	case "friend_of", "collaborates_with":
		return "person"
	case "has_father", "has_mother", "has_sibling", "has_partner":
		return "person"

	// Media & entertainment
	case "watched":
		return "movie_show"
	case "read":
		return "book"
	case "played":
		return "game"
	case "listens_to":
		return "music"

	// Interest
	case "interested_in":
		return "interest"

	// Event
	case "attended":
		return "event"

	// Lifestyle
	case "has_pet":
		return "pet"

	default:
		return "unknown"
	}
}
