package server

import (
	"net/http"
	"strconv"

	"chishenme/internal/store"
)

func (s *Server) week(w http.ResponseWriter, r *http.Request, _ store.User) {
	week, err := s.store.Week(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, week)
}

func (s *Server) createDish(w http.ResponseWriter, r *http.Request, _ store.User) {
	var in store.DishInput
	if !decodeBody(w, r, &in) {
		return
	}
	dish, err := s.store.CreateDish(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dish)
}

func (s *Server) updateDish(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.DishInput
	if !decodeBody(w, r, &in) {
		return
	}
	dish, err := s.store.UpdateDish(id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

func (s *Server) deleteDish(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteDish(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) cookbook(w http.ResponseWriter, _ *http.Request, _ store.User) {
	recipes, err := s.store.Recipes()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipes)
}

func (s *Server) saveRecipe(w http.ResponseWriter, r *http.Request, _ store.User) {
	var in store.RecipeInput
	if !decodeBody(w, r, &in) {
		return
	}
	recipe, err := s.store.SaveRecipe(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) updateRecipe(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.RecipeInput
	if !decodeBody(w, r, &in) {
		return
	}
	recipe, err := s.store.UpdateRecipe(id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func (s *Server) deleteRecipe(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteRecipe(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) pantry(w http.ResponseWriter, _ *http.Request, _ store.User) {
	items, err := s.store.Pantry()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) addPantry(w http.ResponseWriter, r *http.Request, _ store.User) {
	var in store.IngredientInput
	if !decodeBody(w, r, &in) {
		return
	}
	item, err := s.store.AddPantry(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updatePantry(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in store.IngredientInput
	if !decodeBody(w, r, &in) {
		return
	}
	item, err := s.store.UpdatePantry(id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deletePantry(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.store.DeletePantry(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) allocate(w http.ResponseWriter, r *http.Request, _ store.User) {
	var body struct {
		PantryID int64 `json:"pantry_id"`
		DishID   int64 `json:"dish_id"`
		Amount   int   `json:"amount"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.Allocate(body.PantryID, body.DishID, body.Amount); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) returnToPantry(w http.ResponseWriter, r *http.Request, _ store.User) {
	var body struct {
		IngredientID int64 `json:"ingredient_id"`
		Amount       int   `json:"amount"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.ReturnToPantry(body.IngredientID, body.Amount); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) setReady(w http.ResponseWriter, r *http.Request, _ store.User) {
	var body struct {
		IDs   []int64 `json:"ids"`
		Ready bool    `json:"ready"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.SetReady(body.IDs, body.Ready); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeErr(w, http.StatusBadRequest, "编号不对")
		return 0, false
	}
	return id, true
}
