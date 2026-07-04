package memory

import (
	"strings"
	"time"
)

var exclusivePredicateGroups = map[string]string{
	// 位置 — 只能在一个地方
	"lives_in":      "current_location",
	"currently_in":  "current_location",

	// 教育 — 只能在一所学校
	"studies_at":    "current_school",
	"majors_in":     "current_major",

	// 职业 — 只能在一家公司
	"works_at":      "current_company",

	// 技能栈 — 主语言、主工具
	"main_language": "primary_tech_stack",
	"main_tool":     "primary_tech_stack",

	// 偏好 — likes 和 dislikes 不互斥，所以没有分组
	// hates 是 dislikes 的加强版，放在一起
	"hates":         "negative_preference",
	"dislikes":      "negative_preference",

	// 家庭 — has_partner 排他由 Exclusive=true 自身处理
	// 但 father/mother/sibling 不需要跨谓语排他

	// 目标 — plans_to 和 goal_is 不互斥
}

func normalizePredicate(predicate string) string {
	return strings.TrimSpace(strings.ToLower(predicate))
}

func normalizeEntityName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func exclusiveGroup(predicate string) string {
	return exclusivePredicateGroups[normalizePredicate(predicate)]
}

func predicatesInGroup(group string) []string {
	if group == "" {
		return nil
	}
	predicates := make([]string, 0)
	for predicate, g := range exclusivePredicateGroups {
		if g == group {
			predicates = append(predicates, predicate)
		}
	}
	return predicates
}

func isHistoricalQuery(query string) bool {
	keywords := []string{"以前", "之前", "曾经", "过去", "后来", "当时", "上次", "还记得", "改成", "原来", "以前说过"}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			return true
		}
	}
	return false
}

func resolveTemporal(sentAt time.Time, hint string) (time.Time, *time.Time) {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
	return sentAt, nil
}

func buildSearchText(f Fact) string {
	parts := []string{f.Object, normalizeEntityName(f.Object), f.EntityType, f.Predicate, f.Category, f.Content, f.Evidence}
	return strings.Join(compactStrings(parts), " ")
}

func compactStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}
