package store

import (
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "meals.db"), Admin{Username: "dev", Password: "devdevdev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAllocateAndReturn(t *testing.T) {
	s := openTest(t)
	pantry, err := s.AddPantry(IngredientInput{Name: "番茄", Amount: 500, Unit: "克"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPantry(IngredientInput{Name: "番茄", Amount: 20, Unit: "g"}); err != nil {
		t.Fatal(err)
	}
	home, err := s.Pantry()
	if err != nil {
		t.Fatal(err)
	}
	if len(home) != 1 || home[0].Amount != 520 {
		t.Fatalf("pantry merge = %+v", home)
	}
	pantry = home[0]

	dish, err := s.CreateDish(DishInput{
		Day: "2026-11-23", Slot: "晚", Name: "番茄炒蛋", Servings: "2人",
		Ingredients: []IngredientInput{
			{Name: "番茄", Amount: 300, Unit: "g"},
			{Name: "盐", Amount: 3, Unit: "g"},
			{Name: "番茄", Amount: 50, Unit: "g"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dish.Ingredients[0].Amount != 350 || dish.Ingredients[0].Ready {
		t.Fatalf("to-buy lines were not combined: %+v", dish.Ingredients)
	}

	if err := s.Allocate(pantry.ID, dish.ID, 200); err != nil {
		t.Fatal(err)
	}
	dish, err = s.Dish(dish.ID)
	if err != nil {
		t.Fatal(err)
	}
	need, have := findBoth(t, dish, "番茄")
	if need.Amount != 150 || have.Amount != 200 {
		t.Fatalf("after partial allocate need=%+v have=%+v", need, have)
	}
	home, _ = s.Pantry()
	if home[0].Amount != 320 {
		t.Fatalf("pantry after allocate = %+v", home)
	}

	if err := s.Allocate(home[0].ID, dish.ID, 320); err != nil {
		t.Fatal(err)
	}
	dish, _ = s.Dish(dish.ID)
	if _, ok := findReady(dish, "番茄", false); ok {
		t.Fatal("shopping line should be gone once the cupboard covered it")
	}
	ready, ok := findReady(dish, "番茄", true)
	if !ok || ready.Amount != 520 {
		t.Fatalf("ready tomato = %+v ok=%v", ready, ok)
	}
	home, _ = s.Pantry()
	if len(home) != 0 {
		t.Fatalf("pantry should be empty, got %+v", home)
	}

	if err := s.Allocate(pantry.ID, dish.ID, 1); err != ErrNotFound {
		t.Fatalf("allocate missing pantry = %v", err)
	}
	dish, _ = s.Dish(dish.ID)
	ready, _ = findReady(dish, "番茄", true)
	if err := s.ReturnToPantry(ready.ID, 100); err != nil {
		t.Fatal(err)
	}
	dish, _ = s.Dish(dish.ID)
	ready, _ = findReady(dish, "番茄", true)
	if ready.Amount != 420 {
		t.Fatalf("ready after return = %+v", ready)
	}
	home, _ = s.Pantry()
	if len(home) != 1 || home[0].Amount != 100 {
		t.Fatalf("pantry after return = %+v", home)
	}

	salt, ok := findReady(dish, "盐", false)
	if !ok {
		t.Fatal("salt missing")
	}
	if err := s.ReturnToPantry(salt.ID, 1); err == nil {
		t.Fatal("returning a shopping line should fail")
	}
}

func TestReadyMergesAndDeleteKeepsPantry(t *testing.T) {
	s := openTest(t)
	if _, err := s.AddPantry(IngredientInput{Name: "鸡蛋", Amount: 4, Unit: "个"}); err != nil {
		t.Fatal(err)
	}
	dish, err := s.CreateDish(DishInput{
		Day: "2026-11-24", Slot: "早餐", Name: "煎蛋",
		Ingredients: []IngredientInput{
			{Name: "鸡蛋", Amount: 2, Unit: "个"},
			{Name: "鸡蛋", Amount: 1, Unit: "个", Ready: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	need, _ := findReady(dish, "鸡蛋", false)
	if err := s.SetReady([]int64{need.ID}, true); err != nil {
		t.Fatal(err)
	}
	dish, _ = s.Dish(dish.ID)
	if len(dish.Ingredients) != 1 || !dish.Ingredients[0].Ready || dish.Ingredients[0].Amount != 3 {
		t.Fatalf("merged ready = %+v", dish.Ingredients)
	}
	if err := s.DeleteDish(dish.ID); err != nil {
		t.Fatal(err)
	}
	home, _ := s.Pantry()
	if len(home) != 1 || home[0].Amount != 4 {
		t.Fatalf("pantry survived delete? %+v", home)
	}
	week, err := s.Week("2026-11-23", "2026-11-29")
	if err != nil {
		t.Fatal(err)
	}
	if len(week.Dishes) != 0 {
		t.Fatalf("week = %+v", week.Dishes)
	}
}

func TestRecipeKeepsADishCopy(t *testing.T) {
	s := openTest(t)
	saved, err := s.SaveRecipe(RecipeInput{
		Name: "番茄炒蛋", Servings: "2人", Steps: "先炒蛋",
		Ingredients: []IngredientInput{
			{Name: "番茄", Amount: 100, Unit: "g"},
			{Name: "番茄", Amount: 200, Unit: "克", Ready: true},
			{Name: "鸡蛋", Amount: 2, Unit: "个", Ready: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Ingredients) != 2 || saved.Ingredients[0].Amount != 300 || saved.Ingredients[0].Ready {
		t.Fatalf("recipe should merge the tomato and forget ready: %+v", saved.Ingredients)
	}
	again, err := s.SaveRecipe(RecipeInput{Name: "番茄炒蛋", Servings: "3人", Ingredients: []IngredientInput{{Name: "鸡蛋", Amount: 3, Unit: "个"}}})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != saved.ID || again.Servings != "3人" || len(again.Ingredients) != 1 {
		t.Fatalf("same name should replace the recipe: %+v", again)
	}
	list, err := s.Recipes()
	if err != nil || len(list) != 1 || list[0].Ingredients[0].Name != "鸡蛋" {
		t.Fatalf("list = %+v %v", list, err)
	}
	if _, err := s.UpdateRecipe(saved.ID, RecipeInput{Name: "番茄炒蛋", Servings: "2人"}); err != nil {
		t.Fatal(err)
	}
	other, err := s.SaveRecipe(RecipeInput{Name: "青菜汤"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateRecipe(other.ID, RecipeInput{Name: "番茄炒蛋"}); err == nil {
		t.Fatal("renaming onto an existing recipe should fail")
	}
	if err := s.DeleteRecipe(other.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.Recipes()
	if len(list) != 1 {
		t.Fatalf("after delete = %+v", list)
	}
}

func TestRejectsBadInput(t *testing.T) {
	s := openTest(t)
	_, err := s.CreateDish(DishInput{Day: "2026-02-31", Slot: "晚", Name: "汤"})
	if err == nil {
		t.Fatal("expected a bad date to fail")
	}
	_, err = s.CreateDish(DishInput{Day: "2026-11-23", Slot: "夜宵", Name: "汤"})
	if err == nil {
		t.Fatal("expected a bad slot to fail")
	}
	if _, err := s.AddPantry(IngredientInput{Name: "盐", Amount: 1, Unit: "勺"}); err == nil {
		t.Fatal("expected a bad unit to fail")
	}
	if _, err := s.Week("2026-11-23", "2026-12-30"); err == nil {
		t.Fatal("expected a long range to fail")
	}
}

func TestPassword(t *testing.T) {
	s := openTest(t)
	if _, err := s.Authenticate("dev", "nope-nope"); err != ErrBadCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := s.Authenticate("dev", "devdevdev"); err != nil {
		t.Fatal(err)
	}
	user, err := s.User(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(user.ID, "devdevdev", "newpass12"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("dev", "devdevdev"); err != ErrBadCredentials {
		t.Fatal("old password still worked")
	}
	if _, err := s.Authenticate("dev", "newpass12"); err != nil {
		t.Fatal(err)
	}
}

func findBoth(t *testing.T, dish Dish, name string) (Ingredient, Ingredient) {
	t.Helper()
	need, okNeed := findReady(dish, name, false)
	have, okHave := findReady(dish, name, true)
	if !okNeed || !okHave {
		t.Fatalf("missing tomato lines in %+v", dish.Ingredients)
	}
	return need, have
}

func findReady(dish Dish, name string, ready bool) (Ingredient, bool) {
	for _, item := range dish.Ingredients {
		if item.Name == name && item.Ready == ready {
			return item, true
		}
	}
	return Ingredient{}, false
}
