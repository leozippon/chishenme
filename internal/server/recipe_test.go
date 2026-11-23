package server

import "testing"

func TestParseRecipe(t *testing.T) {
	body := "```json\n" + `{
		"name": "番茄炒蛋",
		"servings": "2人",
		"steps": "番茄炒软，倒入蛋液。",
		"ingredients": [
			{"name": "番茄", "amount": 2, "unit": "个"},
			{"name": "鸡蛋", "amount": "3", "unit": "只"},
			{"name": "食用油", "amount": 1.5, "unit": "斤"},
			{"name": "盐", "amount": 2, "unit": "克"},
			{"name": "神秘调料", "amount": 1, "unit": "勺"}
		]
	}` + "\n```"
	got, err := parseRecipe(body, "番茄炒蛋")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "番茄炒蛋" || got.Servings != "2人" {
		t.Fatalf("header = %+v", got)
	}
	want := map[string]struct {
		amount int
		unit   string
	}{
		"番茄":  {2, "个"},
		"鸡蛋":  {3, "个"},
		"食用油": {750, "g"},
		"盐":   {2, "g"},
	}
	if len(got.Ingredients) != len(want) {
		t.Fatalf("ingredients = %+v", got.Ingredients)
	}
	for _, item := range got.Ingredients {
		spec, ok := want[item.Name]
		if !ok || item.Amount != spec.amount || item.Unit != spec.unit || item.Ready {
			t.Fatalf("ingredient %+v", item)
		}
	}
}

func TestParseRecipeKeepsAskedName(t *testing.T) {
	got, err := parseRecipe(`{"name":"","steps":"","ingredients":[{"name":"青菜","amount":200,"unit":"g"}]}`, "清炒青菜")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "清炒青菜" || got.Servings != "2人" || len(got.Ingredients) != 1 {
		t.Fatalf("%+v", got)
	}
}
