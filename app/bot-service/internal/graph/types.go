package graph

import "fmt"

// KbDocument is a knowledge-domain result, not an assistant trajectory block.
type KbDocument struct {
	ChunkID        string
	DocID          string
	Title          string
	Content        string
	MatchedContent string
	Score          float64
	KbID           string
	KbName         string
}
type BoundKB struct {
	KBID string
	Mode string
	Name string
}

func FormatKnowledge(docs []KbDocument) string {
	out := ""
	for i, d := range docs {
		out += fmt.Sprintf("【参考资料 %d】来源: %s\n%s\n\n", i+1, d.Title, d.Content)
	}
	return out
}
func LocalePrompt(locale string) string {
	switch locale {
	case "en-US":
		return "Please respond in English unless the user explicitly requests otherwise."
	case "zh-CN":
		return "请用中文回复，除非用户明确要求使用其他语言。"
	case "ja-JP":
		return "特に明示されない限り、日本語で応答してください。"
	case "ko-KR":
		return "사용자가 명시적으로 요청하지 않는 한 한국어로 응답하십시오."
	default:
		return ""
	}
}
