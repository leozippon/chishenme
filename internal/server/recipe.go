package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"chishenme/internal/store"
)

const (
	deepSeekModel   = "deepseek-flash"
	deepSeekDefault = "https://api.deepseek.com"
)

// Recipe is a suggestion. Nothing is saved until the user keeps it.
type Recipe struct {
	Name        string                  `json:"name"`
	Servings    string                  `json:"servings"`
	Steps       string                  `json:"steps"`
	Ingredients []store.IngredientInput `json:"ingredients"`
}

func (s *Server) recipe(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.cfg.DeepSeekKey == "" {
		writeErr(w, http.StatusServiceUnavailable, "还没有配置菜谱生成")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name := strings.Join(strings.Fields(body.Name), " ")
	if name == "" || utf8.RuneCountInString(name) > 24 {
		writeErr(w, http.StatusBadRequest, "先写下菜名，最多 24 个字")
		return
	}
	pantry, err := s.store.Pantry()
	if err != nil {
		s.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 55*time.Second)
	defer cancel()
	content, err := s.callDeepSeek(ctx, name, pantry)
	if err != nil {
		log.Printf("chishenme: recipe: %v", err)
		writeErr(w, http.StatusBadGateway, "菜谱没有生成，可以先手写食材")
		return
	}
	recipe, err := parseRecipe(content, name)
	if err != nil {
		log.Printf("chishenme: recipe parse: %v", err)
		writeErr(w, http.StatusBadGateway, "菜谱没有生成，可以先手写食材")
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) callDeepSeek(ctx context.Context, name string, pantry []store.Ingredient) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"model": deepSeekModel,
		"messages": []map[string]any{
			{"role": "system", "content": recipeSystem()},
			{"role": "user", "content": recipeUser(name, pantry)},
		},
		"response_format": map[string]string{"type": "json_object"},
		"thinking":        map[string]string{"type": "disabled"},
		"max_tokens":      1500,
	})
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(s.cfg.DeepSeekURL, "/")
	if base == "" {
		base = deepSeekDefault
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.DeepSeekKey)
	client := &http.Client{Timeout: 55 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("deepseek status %d: %s", resp.StatusCode, clip(apiError(body), 160))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", err
	}
	if len(envelope.Choices) == 0 {
		return "", fmt.Errorf("empty deepseek response")
	}
	content := strings.TrimSpace(envelope.Choices[0].Message.Content)
	if content == "" {
		content = strings.TrimSpace(envelope.Choices[0].Message.Reasoning)
	}
	if content == "" {
		return "", fmt.Errorf("empty deepseek content")
	}
	return content, nil
}

func recipeSystem() string {
	return `你是家庭做菜助手，按两个人的家常口味给菜谱。只返回一个 JSON 对象，不要 Markdown，不要解释。格式：
{"name":"菜名","servings":"2人","steps":"两三句做法，写清顺序和火候","ingredients":[{"name":"番茄","amount":300,"unit":"g"}]}
规则：
- unit 只能是 g、ml、个。固体用 g，液体用 ml，鸡蛋和按个卖的用个。
- amount 是正整数。不要写适量或少许，盐、油、生抽也要给出克或毫升。做法里同样不要写适量或少许，和食材清单一致。
- 食材名用买菜时的说法，不要写品牌，不要加括号。
- 给出这道菜真正需要的全部食材。不要因为家里有就删掉。
- 如果库存里已有同一种食材，名字必须沿用库存里的写法。`
}

func recipeUser(name string, pantry []store.Ingredient) string {
	var b strings.Builder
	fmt.Fprintf(&b, "菜名：%s\n", name)
	if len(pantry) == 0 {
		b.WriteString("家里库存：无")
		return b.String()
	}
	b.WriteString("家里库存：\n")
	limit := len(pantry)
	if limit > 40 {
		limit = 40
	}
	for _, item := range pantry[:limit] {
		fmt.Fprintf(&b, "- %s %d%s\n", item.Name, item.Amount, store.UnitLabel(item.Unit))
	}
	return b.String()
}

func parseRecipe(content, fallbackName string) (Recipe, error) {
	var raw struct {
		Name        string `json:"name"`
		Servings    string `json:"servings"`
		Steps       string `json:"steps"`
		Ingredients []struct {
			Name   string          `json:"name"`
			Amount json.RawMessage `json:"amount"`
			Unit   string          `json:"unit"`
		} `json:"ingredients"`
	}
	if err := json.Unmarshal([]byte(extractJSON(content)), &raw); err != nil {
		return Recipe{}, fmt.Errorf("parse model json: %w", err)
	}
	name := clip(strings.Join(strings.Fields(raw.Name), " "), 24)
	if name == "" {
		name = fallbackName
	}
	items := make([]store.IngredientInput, 0, len(raw.Ingredients))
	seen := map[string]int{}
	for _, item := range raw.Ingredients {
		if len(items) >= 24 {
			break
		}
		itemName := clip(strings.Join(strings.Fields(item.Name), " "), 20)
		amount, unit, ok := canonAmount(item.Amount, item.Unit)
		if itemName == "" || !ok {
			continue
		}
		key := itemName + "\x00" + unit
		if at, ok := seen[key]; ok {
			sum := items[at].Amount + amount
			if sum > 100000 {
				sum = 100000
			}
			items[at].Amount = sum
			continue
		}
		seen[key] = len(items)
		items = append(items, store.IngredientInput{Name: itemName, Amount: amount, Unit: unit})
	}
	if len(items) == 0 {
		return Recipe{}, fmt.Errorf("recipe has no usable ingredients")
	}
	servings := clip(strings.Join(strings.Fields(raw.Servings), " "), 16)
	if servings == "" {
		servings = "2人"
	}
	return Recipe{
		Name:        name,
		Servings:    servings,
		Steps:       clip(strings.Join(strings.Fields(raw.Steps), " "), 800),
		Ingredients: items,
	}, nil
}

func canonAmount(raw json.RawMessage, unit string) (int, string, bool) {
	amount, ok := rawAmount(raw)
	if !ok {
		return 0, "", false
	}
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "g", "克", "gram", "grams":
		return scale(amount, 1, "g")
	case "kg", "千克", "公斤":
		return scale(amount, 1000, "g")
	case "斤":
		return scale(amount, 500, "g")
	case "两":
		return scale(amount, 50, "g")
	case "ml", "毫升":
		return scale(amount, 1, "ml")
	case "l", "升":
		return scale(amount, 1000, "ml")
	case "个", "只", "枚", "颗", "根", "片", "块":
		return scale(amount, 1, "个")
	default:
		return 0, "", false
	}
}

func scale(amount, factor float64, unit string) (int, string, bool) {
	n := int(math.Round(amount * factor))
	if n < 1 || n > 100000 {
		return 0, "", false
	}
	return n, unit, true
}

func rawAmount(raw json.RawMessage) (float64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f > 0 {
		return f, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(s)
		if _, err := fmt.Sscanf(s, "%f", &f); err == nil && f > 0 {
			return f, true
		}
	}
	return 0, false
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if cut := strings.LastIndex(s, "```"); cut >= 0 {
			s = s[:cut]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func apiError(body []byte) string {
	var wrap struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrap) == nil && wrap.Error.Message != "" {
		return wrap.Error.Message
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty body"
	}
	return text
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
