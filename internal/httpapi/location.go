package httpapi

import (
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

func (api *API) moveSpecimen(w http.ResponseWriter, r *http.Request) {
	var request contracts.MoveRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := api.service.MoveSpecimen(r.Context(), r.PathValue("id"), request)
	if err != nil {
		writeError(w, err)
		return
	}
	// 首次交接返回 201；完全相同的重复提交回放既有记录，返回 200
	// 以明确告诉客户端本次没有产生新的移动记录。
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	} else {
		w.Header().Set("Location", "/specimens/"+r.PathValue("id")+"/moves")
	}
	writeJSON(w, status, result)
}

func (api *API) listMoves(w http.ResponseWriter, r *http.Request) {
	values, err := api.service.ListMoves(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": values})
}
