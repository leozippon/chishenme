// Package store owns the meal plan database: the coming week's dishes, what
// each dish still needs, and the ingredients already at home.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const (
	maxDishName    = 24
	maxItemName    = 20
	maxSteps       = 800
	maxServings    = 16
	maxAmount      = 100000
	maxIngredients = 30
	maxRangeDays   = 31
)

var (
	// ErrNotFound means the addressed row does not exist.
	ErrNotFound = errors.New("record not found")
	// ErrBadCredentials means the username or password did not match.
	ErrBadCredentials = errors.New("invalid credentials")
)

// InvalidError carries a message that is safe to show to the user.
type InvalidError struct{ Msg string }

func (e InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// Ingredient is one named amount. A row with no dish is stock at home.
// Ready is meaningful on a dish: true means that amount is already set aside
// for the dish, false means it still has to be bought.
type Ingredient struct {
	ID     int64  `json:"id"`
	DishID int64  `json:"dish_id,omitempty"`
	Name   string `json:"name"`
	Amount int    `json:"amount"`
	Unit   string `json:"unit"`
	Ready  bool   `json:"ready"`
}

// IngredientInput is an ingredient before it has an id.
type IngredientInput struct {
	Name   string `json:"name"`
	Amount int    `json:"amount"`
	Unit   string `json:"unit"`
	Ready  bool   `json:"ready"`
}

// Dish is one meal on a calendar day.
type Dish struct {
	ID          int64        `json:"id"`
	Day         string       `json:"day"`
	Slot        string       `json:"slot"`
	Name        string       `json:"name"`
	Steps       string       `json:"steps"`
	Servings    string       `json:"servings"`
	Ingredients []Ingredient `json:"ingredients"`
}

// DishInput is the form used to create or replace a dish.
type DishInput struct {
	Day         string            `json:"day"`
	Slot        string            `json:"slot"`
	Name        string            `json:"name"`
	Steps       string            `json:"steps"`
	Servings    string            `json:"servings"`
	Ingredients []IngredientInput `json:"ingredients"`
}

// Week is every dish in a closed date range, oldest day first.
type Week struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Dishes []Dish `json:"dishes"`
}

// Store is a handle on the meal database.
type Store struct {
	db           *sql.DB
	createdAdmin string
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// Open connects to the SQLite file, creates the schema and makes sure an
// administrator exists. An existing database ignores the bootstrap password.
func Open(path string, admin Admin) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is empty")
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureAdmin(admin); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// CreatedAdmin reports the administrator created while opening an empty
// database, or an empty string when an account already existed.
func (s *Store) CreatedAdmin() string { return s.createdAdmin }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			token_version INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS dishes (
			id INTEGER PRIMARY KEY,
			day TEXT NOT NULL,
			slot TEXT NOT NULL,
			name TEXT NOT NULL,
			steps TEXT NOT NULL DEFAULT '',
			servings TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS ingredients (
			id INTEGER PRIMARY KEY,
			dish_id INTEGER REFERENCES dishes(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			amount INTEGER NOT NULL CHECK (amount > 0),
			unit TEXT NOT NULL,
			ready INTEGER NOT NULL DEFAULT 0 CHECK (ready IN (0, 1))
		);
		CREATE INDEX IF NOT EXISTS idx_dishes_day ON dishes(day);
		CREATE INDEX IF NOT EXISTS idx_ingredients_dish ON ingredients(dish_id);
	`)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Week lists dishes and their ingredients for the inclusive date range.
func (s *Store) Week(from, to string) (Week, error) {
	from, to, err := checkRange(from, to)
	if err != nil {
		return Week{}, err
	}
	rows, err := s.db.Query(`
		SELECT id, day, slot, name, steps, servings
		FROM dishes
		WHERE day >= ? AND day <= ?
		ORDER BY day,
			CASE slot WHEN '早' THEN 0 WHEN '午' THEN 1 ELSE 2 END,
			position, id`, from, to)
	if err != nil {
		return Week{}, fmt.Errorf("list dishes: %w", err)
	}
	defer rows.Close()
	dishes := []Dish{}
	ids := []any{}
	index := map[int64]int{}
	for rows.Next() {
		var d Dish
		if err := rows.Scan(&d.ID, &d.Day, &d.Slot, &d.Name, &d.Steps, &d.Servings); err != nil {
			return Week{}, err
		}
		d.Ingredients = []Ingredient{}
		index[d.ID] = len(dishes)
		ids = append(ids, d.ID)
		dishes = append(dishes, d)
	}
	if err := rows.Err(); err != nil {
		return Week{}, err
	}
	if len(ids) > 0 {
		items, err := s.ingredientsFor(s.db, ids)
		if err != nil {
			return Week{}, err
		}
		for _, item := range items {
			at, ok := index[item.DishID]
			if ok {
				dishes[at].Ingredients = append(dishes[at].Ingredients, item)
			}
		}
	}
	return Week{From: from, To: to, Dishes: dishes}, nil
}

func (s *Store) ingredientsFor(q execer, ids []any) ([]Ingredient, error) {
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	rows, err := q.Query(`
		SELECT id, dish_id, name, amount, unit, ready
		FROM ingredients
		WHERE dish_id IN (`+marks+`)
		ORDER BY ready, id`, ids...)
	if err != nil {
		return nil, fmt.Errorf("list ingredients: %w", err)
	}
	defer rows.Close()
	return scanIngredients(rows)
}

// Dish reads one dish with its ingredients.
func (s *Store) Dish(id int64) (Dish, error) {
	return readDish(s.db, id)
}

func readDish(q execer, id int64) (Dish, error) {
	var d Dish
	err := q.QueryRow(`SELECT id, day, slot, name, steps, servings FROM dishes WHERE id = ?`, id).
		Scan(&d.ID, &d.Day, &d.Slot, &d.Name, &d.Steps, &d.Servings)
	if errors.Is(err, sql.ErrNoRows) {
		return Dish{}, ErrNotFound
	}
	if err != nil {
		return Dish{}, fmt.Errorf("read dish: %w", err)
	}
	rows, err := q.Query(`
		SELECT id, dish_id, name, amount, unit, ready
		FROM ingredients WHERE dish_id = ? ORDER BY ready, id`, id)
	if err != nil {
		return Dish{}, fmt.Errorf("read ingredients: %w", err)
	}
	defer rows.Close()
	d.Ingredients, err = scanIngredients(rows)
	if err != nil {
		return Dish{}, err
	}
	if d.Ingredients == nil {
		d.Ingredients = []Ingredient{}
	}
	return d, nil
}

func scanIngredients(rows *sql.Rows) ([]Ingredient, error) {
	out := []Ingredient{}
	for rows.Next() {
		var (
			item  Ingredient
			ready int
		)
		if err := rows.Scan(&item.ID, &item.DishID, &item.Name, &item.Amount, &item.Unit, &ready); err != nil {
			return nil, err
		}
		item.Ready = ready == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

// CreateDish appends a meal to the end of that day and slot.
func (s *Store) CreateDish(in DishInput) (Dish, error) {
	fields, items, err := normalizeDish(in)
	if err != nil {
		return Dish{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Dish{}, err
	}
	defer tx.Rollback()
	var position int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM dishes WHERE day = ? AND slot = ?`, fields.day, fields.slot).Scan(&position); err != nil {
		return Dish{}, fmt.Errorf("next position: %w", err)
	}
	result, err := tx.Exec(`
		INSERT INTO dishes (day, slot, name, steps, servings, position, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		fields.day, fields.slot, fields.name, fields.steps, fields.servings, position, now())
	if err != nil {
		return Dish{}, fmt.Errorf("create dish: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Dish{}, err
	}
	if err := insertIngredients(tx, id, items); err != nil {
		return Dish{}, err
	}
	dish, err := readDish(tx, id)
	if err != nil {
		return Dish{}, err
	}
	return dish, tx.Commit()
}

// UpdateDish replaces the meal's text and its whole ingredient list.
func (s *Store) UpdateDish(id int64, in DishInput) (Dish, error) {
	fields, items, err := normalizeDish(in)
	if err != nil {
		return Dish{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Dish{}, err
	}
	defer tx.Rollback()
	var day, slot string
	var position int
	err = tx.QueryRow(`SELECT day, slot, position FROM dishes WHERE id = ?`, id).Scan(&day, &slot, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return Dish{}, ErrNotFound
	}
	if err != nil {
		return Dish{}, fmt.Errorf("read dish: %w", err)
	}
	if day != fields.day || slot != fields.slot {
		if err := tx.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM dishes WHERE day = ? AND slot = ?`, fields.day, fields.slot).Scan(&position); err != nil {
			return Dish{}, fmt.Errorf("next position: %w", err)
		}
	}
	if _, err := tx.Exec(`UPDATE dishes SET day = ?, slot = ?, name = ?, steps = ?, servings = ?, position = ? WHERE id = ?`,
		fields.day, fields.slot, fields.name, fields.steps, fields.servings, position, id); err != nil {
		return Dish{}, fmt.Errorf("update dish: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ingredients WHERE dish_id = ?`, id); err != nil {
		return Dish{}, fmt.Errorf("replace ingredients: %w", err)
	}
	if err := insertIngredients(tx, id, items); err != nil {
		return Dish{}, err
	}
	dish, err := readDish(tx, id)
	if err != nil {
		return Dish{}, err
	}
	return dish, tx.Commit()
}

// DeleteDish removes a meal and the ingredients listed under it. Stock at
// home is a different row and stays where it is.
func (s *Store) DeleteDish(id int64) error {
	result, err := s.db.Exec(`DELETE FROM dishes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete dish: %w", err)
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

func insertIngredients(tx *sql.Tx, dishID int64, items []IngredientInput) error {
	for _, item := range items {
		ready := 0
		if item.Ready {
			ready = 1
		}
		if _, err := tx.Exec(`
			INSERT INTO ingredients (dish_id, name, amount, unit, ready)
			VALUES (?, ?, ?, ?, ?)`, dishID, item.Name, item.Amount, item.Unit, ready); err != nil {
			return fmt.Errorf("insert ingredient: %w", err)
		}
	}
	return nil
}

// Pantry lists everything currently at home.
func (s *Store) Pantry() ([]Ingredient, error) {
	rows, err := s.db.Query(`
		SELECT id, 0, name, amount, unit, ready
		FROM ingredients WHERE dish_id IS NULL
		ORDER BY name, unit, id`)
	if err != nil {
		return nil, fmt.Errorf("list pantry: %w", err)
	}
	defer rows.Close()
	items, err := scanIngredients(rows)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []Ingredient{}
	}
	return items, nil
}

// AddPantry records stock at home. The same name and unit add onto the existing row.
func (s *Store) AddPantry(in IngredientInput) (Ingredient, error) {
	name, amount, unit, err := checkIngredient(in.Name, in.Amount, in.Unit)
	if err != nil {
		return Ingredient{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Ingredient{}, err
	}
	defer tx.Rollback()
	id, err := addPantryTx(tx, name, amount, unit)
	if err != nil {
		return Ingredient{}, err
	}
	item, err := readPantry(tx, id)
	if err != nil {
		return Ingredient{}, err
	}
	return item, tx.Commit()
}

// UpdatePantry replaces one home row. A rename that collides with another row
// combines the two amounts.
func (s *Store) UpdatePantry(id int64, in IngredientInput) (Ingredient, error) {
	name, amount, unit, err := checkIngredient(in.Name, in.Amount, in.Unit)
	if err != nil {
		return Ingredient{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Ingredient{}, err
	}
	defer tx.Rollback()
	var dish sql.NullInt64
	err = tx.QueryRow(`SELECT dish_id FROM ingredients WHERE id = ?`, id).Scan(&dish)
	if errors.Is(err, sql.ErrNoRows) {
		return Ingredient{}, ErrNotFound
	}
	if err != nil {
		return Ingredient{}, fmt.Errorf("read pantry: %w", err)
	}
	if dish.Valid {
		return Ingredient{}, invalid("这是菜下面的食材，请在那道菜里改")
	}
	var otherID int64
	var otherAmount int
	err = tx.QueryRow(`
		SELECT id, amount FROM ingredients
		WHERE dish_id IS NULL AND name = ? AND unit = ? AND id <> ?`, name, unit, id).
		Scan(&otherID, &otherAmount)
	if err == nil {
		sum, err := addAmount(otherAmount, amount)
		if err != nil {
			return Ingredient{}, err
		}
		if _, err := tx.Exec(`UPDATE ingredients SET amount = ? WHERE id = ?`, sum, otherID); err != nil {
			return Ingredient{}, fmt.Errorf("merge pantry: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM ingredients WHERE id = ?`, id); err != nil {
			return Ingredient{}, fmt.Errorf("merge pantry: %w", err)
		}
		item, err := readPantry(tx, otherID)
		if err != nil {
			return Ingredient{}, err
		}
		return item, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Ingredient{}, fmt.Errorf("find pantry: %w", err)
	}
	if _, err := tx.Exec(`UPDATE ingredients SET name = ?, amount = ?, unit = ?, ready = 1 WHERE id = ?`, name, amount, unit, id); err != nil {
		return Ingredient{}, fmt.Errorf("update pantry: %w", err)
	}
	item, err := readPantry(tx, id)
	if err != nil {
		return Ingredient{}, err
	}
	return item, tx.Commit()
}

// DeletePantry removes one home row.
func (s *Store) DeletePantry(id int64) error {
	result, err := s.db.Exec(`DELETE FROM ingredients WHERE id = ? AND dish_id IS NULL`, id)
	if err != nil {
		return fmt.Errorf("delete pantry: %w", err)
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

// Allocate moves weight from a home row onto a dish. The moved amount becomes
// ready under that dish. A same-named line that was still to be bought shrinks
// by the same amount, so the shopping list does not keep asking for food that
// just came out of the cupboard. Extra weight, beyond what the dish asked for,
// stays on the dish as ready rather than inflating the shopping list.
func (s *Store) Allocate(pantryID, dishID int64, amount int) error {
	if amount < 1 {
		return invalid("份量至少为 1")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		dish sql.NullInt64
		name string
		have int
		unit string
	)
	err = tx.QueryRow(`SELECT dish_id, name, amount, unit FROM ingredients WHERE id = ?`, pantryID).
		Scan(&dish, &name, &have, &unit)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read pantry: %w", err)
	}
	if dish.Valid {
		return invalid("只能从家里的食材往菜里放")
	}
	if amount > have {
		return invalid("家里的%s只剩 %d%s", name, have, UnitLabel(unit))
	}
	var exists int
	err = tx.QueryRow(`SELECT 1 FROM dishes WHERE id = ?`, dishID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read dish: %w", err)
	}
	if err := reduceRow(tx, pantryID, have, amount); err != nil {
		return err
	}
	var buyID, buyAmount int
	err = tx.QueryRow(`
		SELECT id, amount FROM ingredients
		WHERE dish_id = ? AND name = ? AND unit = ? AND ready = 0
		ORDER BY id LIMIT 1`, dishID, name, unit).Scan(&buyID, &buyAmount)
	if err == nil {
		if err := reduceRow(tx, int64(buyID), buyAmount, amount); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read dish ingredient: %w", err)
	}
	if err := addReady(tx, dishID, name, unit, amount); err != nil {
		return err
	}
	return tx.Commit()
}

// ReturnToPantry moves weight from a ready dish line back home. Lines that are
// still to be bought stay put; uncheck or edit those instead.
func (s *Store) ReturnToPantry(ingredientID int64, amount int) error {
	if amount < 1 {
		return invalid("份量至少为 1")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		dishID sql.NullInt64
		name   string
		have   int
		unit   string
		ready  int
	)
	err = tx.QueryRow(`SELECT dish_id, name, amount, unit, ready FROM ingredients WHERE id = ?`, ingredientID).
		Scan(&dishID, &name, &have, &unit, &ready)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read ingredient: %w", err)
	}
	if !dishID.Valid || ready == 0 {
		return invalid("只有已经备好的食材可以放回家里")
	}
	if amount > have {
		return invalid("这道菜上的%s只有 %d%s", name, have, UnitLabel(unit))
	}
	if err := reduceRow(tx, ingredientID, have, amount); err != nil {
		return err
	}
	if _, err := addPantryTx(tx, name, amount, unit); err != nil {
		return err
	}
	return tx.Commit()
}

// SetReady marks dish lines as bought or still needed. Matching lines of the
// same kind on that dish combine, so a dish does not show the same ingredient
// twice. Stock at home is not involved.
func (s *Store) SetReady(ids []int64, ready bool) error {
	if len(ids) == 0 {
		return invalid("没有要更新的食材")
	}
	if len(ids) > 100 {
		return invalid("一次勾选太多了")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	want := 0
	if ready {
		want = 1
	}
	for _, id := range ids {
		var (
			dishID sql.NullInt64
			name   string
			amount int
			unit   string
			cur    int
		)
		err := tx.QueryRow(`SELECT dish_id, name, amount, unit, ready FROM ingredients WHERE id = ?`, id).
			Scan(&dishID, &name, &amount, &unit, &cur)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read ingredient: %w", err)
		}
		if !dishID.Valid {
			return invalid("家里的食材不用勾选")
		}
		if cur == want {
			continue
		}
		var otherID, otherAmount int
		err = tx.QueryRow(`
			SELECT id, amount FROM ingredients
			WHERE dish_id = ? AND name = ? AND unit = ? AND ready = ? AND id <> ?
			ORDER BY id LIMIT 1`, dishID.Int64, name, unit, want, id).Scan(&otherID, &otherAmount)
		if err == nil {
			sum, err := addAmount(otherAmount, amount)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE ingredients SET amount = ? WHERE id = ?`, sum, otherID); err != nil {
				return fmt.Errorf("merge ingredient: %w", err)
			}
			if _, err := tx.Exec(`DELETE FROM ingredients WHERE id = ?`, id); err != nil {
				return fmt.Errorf("merge ingredient: %w", err)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("find ingredient: %w", err)
		}
		if _, err := tx.Exec(`UPDATE ingredients SET ready = ? WHERE id = ?`, want, id); err != nil {
			return fmt.Errorf("update ingredient: %w", err)
		}
	}
	return tx.Commit()
}

func addPantryTx(tx *sql.Tx, name string, amount int, unit string) (int64, error) {
	var id, have int64
	err := tx.QueryRow(`SELECT id, amount FROM ingredients WHERE dish_id IS NULL AND name = ? AND unit = ?`, name, unit).
		Scan(&id, &have)
	if err == nil {
		sum, err := addAmount(int(have), amount)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`UPDATE ingredients SET amount = ? WHERE id = ?`, sum, id); err != nil {
			return 0, fmt.Errorf("add pantry: %w", err)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find pantry: %w", err)
	}
	result, err := tx.Exec(`INSERT INTO ingredients (dish_id, name, amount, unit, ready) VALUES (NULL, ?, ?, ?, 1)`, name, amount, unit)
	if err != nil {
		return 0, fmt.Errorf("add pantry: %w", err)
	}
	return result.LastInsertId()
}

func addReady(tx *sql.Tx, dishID int64, name, unit string, amount int) error {
	var id, have int
	err := tx.QueryRow(`
		SELECT id, amount FROM ingredients
		WHERE dish_id = ? AND name = ? AND unit = ? AND ready = 1
		ORDER BY id LIMIT 1`, dishID, name, unit).Scan(&id, &have)
	if err == nil {
		sum, err := addAmount(have, amount)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE ingredients SET amount = ? WHERE id = ?`, sum, id)
		if err != nil {
			return fmt.Errorf("add ready ingredient: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find ready ingredient: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO ingredients (dish_id, name, amount, unit, ready) VALUES (?, ?, ?, ?, 1)`, dishID, name, amount, unit)
	if err != nil {
		return fmt.Errorf("add ready ingredient: %w", err)
	}
	return nil
}

// reduceRow subtracts amount from a row and deletes the row when the subtraction
// covers it. A larger amount still removes the row: moving 500 g onto a dish
// that only asked for 300 g clears the shopping line, and the whole 500 g is
// recorded separately as ready.
func reduceRow(tx *sql.Tx, id int64, have, amount int) error {
	if amount >= have {
		if _, err := tx.Exec(`DELETE FROM ingredients WHERE id = ?`, id); err != nil {
			return fmt.Errorf("remove ingredient: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(`UPDATE ingredients SET amount = ? WHERE id = ?`, have-amount, id); err != nil {
		return fmt.Errorf("reduce ingredient: %w", err)
	}
	return nil
}

func readPantry(q execer, id int64) (Ingredient, error) {
	var item Ingredient
	err := q.QueryRow(`SELECT id, name, amount, unit FROM ingredients WHERE id = ? AND dish_id IS NULL`, id).
		Scan(&item.ID, &item.Name, &item.Amount, &item.Unit)
	if errors.Is(err, sql.ErrNoRows) {
		return Ingredient{}, ErrNotFound
	}
	if err != nil {
		return Ingredient{}, fmt.Errorf("read pantry: %w", err)
	}
	item.Ready = true
	return item, nil
}

type dishFields struct {
	day, slot, name, steps, servings string
}

func normalizeDish(in DishInput) (dishFields, []IngredientInput, error) {
	day, err := checkDay(in.Day)
	if err != nil {
		return dishFields{}, nil, err
	}
	slot, err := checkSlot(in.Slot)
	if err != nil {
		return dishFields{}, nil, err
	}
	name, err := checkText(in.Name, 1, maxDishName, "菜名")
	if err != nil {
		return dishFields{}, nil, err
	}
	steps := collapse(in.Steps)
	if utf8.RuneCountInString(steps) > maxSteps {
		return dishFields{}, nil, invalid("做法太长了")
	}
	servings := collapse(in.Servings)
	if utf8.RuneCountInString(servings) > maxServings {
		return dishFields{}, nil, invalid("份量说明太长了")
	}
	items, err := coalesce(in.Ingredients)
	if err != nil {
		return dishFields{}, nil, err
	}
	return dishFields{day: day, slot: slot, name: name, steps: steps, servings: servings}, items, nil
}

func coalesce(items []IngredientInput) ([]IngredientInput, error) {
	if len(items) > maxIngredients {
		return nil, invalid("一道菜最多 %d 样食材", maxIngredients)
	}
	type key struct {
		name, unit string
		ready      bool
	}
	order := []key{}
	sums := map[key]int{}
	for _, item := range items {
		name, amount, unit, err := checkIngredient(item.Name, item.Amount, item.Unit)
		if err != nil {
			return nil, err
		}
		k := key{name, unit, item.Ready}
		if _, ok := sums[k]; !ok {
			order = append(order, k)
		}
		sum, err := addAmount(sums[k], amount)
		if err != nil {
			return nil, err
		}
		sums[k] = sum
	}
	out := make([]IngredientInput, 0, len(order))
	for _, k := range order {
		out = append(out, IngredientInput{Name: k.name, Amount: sums[k], Unit: k.unit, Ready: k.ready})
	}
	return out, nil
}

func checkIngredient(name string, amount int, unit string) (string, int, string, error) {
	name, err := checkText(name, 1, maxItemName, "食材名")
	if err != nil {
		return "", 0, "", err
	}
	if amount < 1 {
		return "", 0, "", invalid("「%s」的份量至少为 1", name)
	}
	if amount > maxAmount {
		return "", 0, "", invalid("「%s」的份量太大了", name)
	}
	unit, err = checkUnit(unit)
	if err != nil {
		return "", 0, "", err
	}
	return name, amount, unit, nil
}

func addAmount(a, b int) (int, error) {
	if a < 0 || b < 0 || a > maxAmount-b {
		return 0, invalid("份量太大了")
	}
	return a + b, nil
}

func checkText(value string, min, max int, label string) (string, error) {
	value = collapse(value)
	n := utf8.RuneCountInString(value)
	if n < min || n > max {
		if min == 1 {
			return "", invalid("%s需要 1 到 %d 个字", label, max)
		}
		return "", invalid("%s的长度不对", label)
	}
	return value, nil
}

func collapse(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func checkDay(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return "", invalid("日期格式应为 YYYY-MM-DD")
	}
	return parsed.Format(time.DateOnly), nil
}

func checkRange(from, to string) (string, string, error) {
	from, err := checkDay(from)
	if err != nil {
		return "", "", err
	}
	to, err = checkDay(to)
	if err != nil {
		return "", "", err
	}
	start, _ := time.Parse(time.DateOnly, from)
	end, _ := time.Parse(time.DateOnly, to)
	if end.Before(start) {
		return "", "", invalid("结束日期不能早于开始日期")
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return "", "", invalid("一次最多看 %d 天", maxRangeDays)
	}
	return from, to, nil
}

func checkSlot(value string) (string, error) {
	switch collapse(value) {
	case "早", "早餐":
		return "早", nil
	case "午", "午餐":
		return "午", nil
	case "晚", "晚餐":
		return "晚", nil
	default:
		return "", invalid("请选择早餐、午餐或晚餐")
	}
}

func checkUnit(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "g", "克":
		return "g", nil
	case "ml", "毫升":
		return "ml", nil
	case "个":
		return "个", nil
	default:
		return "", invalid("单位用克、毫升或个")
	}
}

// UnitLabel is the word shown next to an amount.
func UnitLabel(unit string) string {
	switch unit {
	case "g":
		return "克"
	case "ml":
		return "毫升"
	default:
		return "个"
	}
}

func now() string { return time.Now().Format(time.RFC3339) }
