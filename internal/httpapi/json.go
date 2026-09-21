package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var rule store.RuleError
	var conflict store.ConflictError
	switch {
	case errors.As(err, &rule):
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{Error: string(rule)})
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, errorBody{Error: string(conflict)})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{Error: err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "服务器内部错误"})
	}
}

// decodeJSON 严格解析请求体，拒绝未知字段。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "请求体不是合法 JSON: " + err.Error()})
		return false
	}
	return true
}
