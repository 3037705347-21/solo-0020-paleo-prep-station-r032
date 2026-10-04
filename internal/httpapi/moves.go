package httpapi

import (
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) moveSpecimen(w http.ResponseWriter, r *http.Request) {
	var request contracts.MoveSpecimenRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	record, created, err := api.service.MoveSpecimen(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, record)
		return
	}
	writeJSON(w, http.StatusCreated, record)
}

func (api *API) listMovements(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListMovements(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}
