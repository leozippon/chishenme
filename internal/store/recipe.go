package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Recipe is a saved dish that is not tied to a day. Importing one copies its
// name, method and ingredients into a new meal; the copy starts out unbought.
type Recipe struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Steps       string            `json:"steps"`
	Servings    string            `json:"servings"`
	Ingredients []IngredientInput `json:"ingredients"`
}

// RecipeInput is the form used to save or replace a recipe.
type RecipeInput struct {
	Name        string            `json:"name"`
	Steps       string            `json:"steps"`
	Servings    string            `json:"servings"`
	Ingredients []IngredientInput `json:"ingredients"`
}

// Recipes lists every saved recipe, by name.
func (s *Store) Recipes() ([]Recipe, error) {
	rows, err := s.db.Query(`SELECT id, name, steps, servings FROM recipes ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list recipes: %w", err)
	}
	defer rows.Close()
	out := []Recipe{}
	index := map[int64]int{}
	ids := []any{}
	for rows.Next() {
		var recipe Recipe
		if err := rows.Scan(&recipe.ID, &recipe.Name, &recipe.Steps, &recipe.Servings); err != nil {
			return nil, err
		}
		recipe.Ingredients = []IngredientInput{}
		index[recipe.ID] = len(out)
		ids = append(ids, recipe.ID)
		out = append(out, recipe)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	items, err := s.db.Query(`
		SELECT recipe_id, name, amount, unit
		FROM recipe_ingredients
		WHERE recipe_id IN (`+marks+`)
		ORDER BY id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("list recipe ingredients: %w", err)
	}
	defer items.Close()
	for items.Next() {
		var (
			recipeID int64
			item     IngredientInput
		)
		if err := items.Scan(&recipeID, &item.Name, &item.Amount, &item.Unit); err != nil {
			return nil, err
		}
		if at, ok := index[recipeID]; ok {
			out[at].Ingredients = append(out[at].Ingredients, item)
		}
	}
	return out, items.Err()
}

// SaveRecipe stores a recipe under its name. Saving the same name again
// replaces the method and the ingredient list, so a dish can refresh the
// recipe it came from.
func (s *Store) SaveRecipe(in RecipeInput) (Recipe, error) {
	name, steps, servings, items, err := normalizeRecipe(in)
	if err != nil {
		return Recipe{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Recipe{}, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRow(`SELECT id FROM recipes WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		result, err := tx.Exec(`INSERT INTO recipes (name, steps, servings, created_at) VALUES (?, ?, ?, ?)`, name, steps, servings, now())
		if err != nil {
			return Recipe{}, fmt.Errorf("create recipe: %w", err)
		}
		id, err = result.LastInsertId()
		if err != nil {
			return Recipe{}, err
		}
	} else if err != nil {
		return Recipe{}, fmt.Errorf("read recipe: %w", err)
	} else if _, err := tx.Exec(`UPDATE recipes SET steps = ?, servings = ? WHERE id = ?`, steps, servings, id); err != nil {
		return Recipe{}, fmt.Errorf("update recipe: %w", err)
	}
	if err := replaceRecipeIngredients(tx, id, items); err != nil {
		return Recipe{}, err
	}
	recipe, err := readRecipe(tx, id)
	if err != nil {
		return Recipe{}, err
	}
	return recipe, tx.Commit()
}

// UpdateRecipe replaces one saved recipe. Renaming onto a name that already
// exists is refused, so two recipes do not silently collapse.
func (s *Store) UpdateRecipe(id int64, in RecipeInput) (Recipe, error) {
	name, steps, servings, items, err := normalizeRecipe(in)
	if err != nil {
		return Recipe{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Recipe{}, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRow(`SELECT id FROM recipes WHERE id = ?`, id).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrNotFound
	}
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe: %w", err)
	}
	if _, err := tx.Exec(`UPDATE recipes SET name = ?, steps = ?, servings = ? WHERE id = ?`, name, steps, servings, id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Recipe{}, invalid("已经有同名菜谱")
		}
		return Recipe{}, fmt.Errorf("update recipe: %w", err)
	}
	if err := replaceRecipeIngredients(tx, id, items); err != nil {
		return Recipe{}, err
	}
	recipe, err := readRecipe(tx, id)
	if err != nil {
		return Recipe{}, err
	}
	return recipe, tx.Commit()
}

// DeleteRecipe removes a saved recipe. Meals already copied from it stay.
func (s *Store) DeleteRecipe(id int64) error {
	result, err := s.db.Exec(`DELETE FROM recipes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete recipe: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func replaceRecipeIngredients(tx *sql.Tx, id int64, items []IngredientInput) error {
	if _, err := tx.Exec(`DELETE FROM recipe_ingredients WHERE recipe_id = ?`, id); err != nil {
		return fmt.Errorf("replace recipe ingredients: %w", err)
	}
	for _, item := range items {
		if _, err := tx.Exec(`
			INSERT INTO recipe_ingredients (recipe_id, name, amount, unit) VALUES (?, ?, ?, ?)`,
			id, item.Name, item.Amount, item.Unit); err != nil {
			return fmt.Errorf("insert recipe ingredient: %w", err)
		}
	}
	return nil
}

func readRecipe(q execer, id int64) (Recipe, error) {
	var recipe Recipe
	err := q.QueryRow(`SELECT id, name, steps, servings FROM recipes WHERE id = ?`, id).
		Scan(&recipe.ID, &recipe.Name, &recipe.Steps, &recipe.Servings)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, ErrNotFound
	}
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe: %w", err)
	}
	rows, err := q.Query(`SELECT name, amount, unit FROM recipe_ingredients WHERE recipe_id = ? ORDER BY id`, id)
	if err != nil {
		return Recipe{}, fmt.Errorf("read recipe ingredients: %w", err)
	}
	defer rows.Close()
	recipe.Ingredients = []IngredientInput{}
	for rows.Next() {
		var item IngredientInput
		if err := rows.Scan(&item.Name, &item.Amount, &item.Unit); err != nil {
			return Recipe{}, err
		}
		recipe.Ingredients = append(recipe.Ingredients, item)
	}
	return recipe, rows.Err()
}

func normalizeRecipe(in RecipeInput) (name, steps, servings string, items []IngredientInput, err error) {
	name, err = checkText(in.Name, 1, maxDishName, "菜名")
	if err != nil {
		return "", "", "", nil, err
	}
	steps = collapse(in.Steps)
	if utf8.RuneCountInString(steps) > maxSteps {
		return "", "", "", nil, invalid("做法太长了")
	}
	servings = collapse(in.Servings)
	if utf8.RuneCountInString(servings) > maxServings {
		return "", "", "", nil, invalid("份量说明太长了")
	}
	flat := make([]IngredientInput, len(in.Ingredients))
	for i, item := range in.Ingredients {
		flat[i] = item
		flat[i].Ready = false
	}
	items, err = coalesce(flat)
	return name, steps, servings, items, err
}
