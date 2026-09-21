package httpapi

import (
	"net/http"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

// Router 组装过坝链路核验的全部 HTTP 路由。
func Router(s *store.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /v1/stages", func(w http.ResponseWriter, _ *http.Request) {
		type stage struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		out := make([]stage, 0, len(domain.OrderedStages))
		for _, st := range domain.OrderedStages {
			out = append(out, stage{Code: string(st), Name: domain.StageNames[st]})
		}
		writeJSON(w, http.StatusOK, map[string]any{"stages": out})
	})

	// 鱼批次
	mux.HandleFunc("POST /v1/lots", func(w http.ResponseWriter, r *http.Request) {
		var in store.CreateLotInput
		if !decodeJSON(w, r, &in) {
			return
		}
		lot, err := s.CreateLot(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, lot)
	})
	mux.HandleFunc("GET /v1/lots", func(w http.ResponseWriter, r *http.Request) {
		lots, err := s.ListLots(r.Context(), r.URL.Query().Get("origin"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"lots": lots})
	})
	mux.HandleFunc("GET /v1/lots/{lotRef}", func(w http.ResponseWriter, r *http.Request) {
		lot, err := s.GetLot(r.Context(), r.PathValue("lotRef"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lot)
	})
	mux.HandleFunc("GET /v1/lots/{lotRef}/trace", func(w http.ResponseWriter, r *http.Request) {
		trace, err := s.TraceLot(r.Context(), r.PathValue("lotRef"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, trace)
	})

	// 逐段交接
	mux.HandleFunc("POST /v1/handovers", func(w http.ResponseWriter, r *http.Request) {
		var in store.DispatchInput
		if !decodeJSON(w, r, &in) {
			return
		}
		h, err := s.Dispatch(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h)
	})
	mux.HandleFunc("POST /v1/handovers/{id}/receive", func(w http.ResponseWriter, r *http.Request) {
		var in store.ReceiveInput
		if !decodeJSON(w, r, &in) {
			return
		}
		in.HandoverID = r.PathValue("id")
		h, err := s.Receive(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("GET /v1/handovers/{id}", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.GetHandover(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
	})
	mux.HandleFunc("GET /v1/handovers/{id}/reconciliation", func(w http.ResponseWriter, r *http.Request) {
		rec, err := s.ReconcileHandover(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rec)
	})

	// 死亡登记与确认
	mux.HandleFunc("POST /v1/losses", func(w http.ResponseWriter, r *http.Request) {
		var in store.LossInput
		if !decodeJSON(w, r, &in) {
			return
		}
		e, err := s.RegisterLoss(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, e)
	})
	mux.HandleFunc("POST /v1/losses/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConfirmedBy string `json:"confirmed_by"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		e, err := s.ConfirmLoss(r.Context(), r.PathValue("id"), body.ConfirmedBy)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	})
	mux.HandleFunc("POST /v1/losses/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConfirmedBy string `json:"confirmed_by"`
			Note        string `json:"note"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		e, err := s.RejectLoss(r.Context(), r.PathValue("id"), body.ConfirmedBy, body.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	})

	// 重新分拣
	mux.HandleFunc("POST /v1/resorts", func(w http.ResponseWriter, r *http.Request) {
		var in store.ResortInput
		if !decodeJSON(w, r, &in) {
			return
		}
		ev, err := s.RegisterResort(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, ev)
	})
	mux.HandleFunc("POST /v1/resorts/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConfirmedBy string `json:"confirmed_by"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		ev, err := s.ConfirmResort(r.Context(), r.PathValue("id"), body.ConfirmedBy)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ev)
	})
	mux.HandleFunc("POST /v1/resorts/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConfirmedBy string `json:"confirmed_by"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		ev, err := s.RejectResort(r.Context(), r.PathValue("id"), body.ConfirmedBy)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ev)
	})

	// 设备故障
	mux.HandleFunc("POST /v1/faults", func(w http.ResponseWriter, r *http.Request) {
		var in store.FaultInput
		if !decodeJSON(w, r, &in) {
			return
		}
		f, err := s.RegisterFault(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	})
	mux.HandleFunc("POST /v1/faults/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Note string `json:"note"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		f, err := s.ResolveFault(r.Context(), r.PathValue("id"), body.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
	mux.HandleFunc("GET /v1/faults", func(w http.ResponseWriter, r *http.Request) {
		faults, err := s.ListFaults(r.Context(), r.URL.Query().Get("lot_ref"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"faults": faults})
	})

	// 放流
	mux.HandleFunc("POST /v1/releases", func(w http.ResponseWriter, r *http.Request) {
		var in store.ReleaseInput
		if !decodeJSON(w, r, &in) {
			return
		}
		rel, err := s.CreateRelease(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, rel)
	})

	// 标记
	mux.HandleFunc("POST /v1/marks", func(w http.ResponseWriter, r *http.Request) {
		var in store.MarkInput
		if !decodeJSON(w, r, &in) {
			return
		}
		m, err := s.AddMark(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, m)
	})

	// 放流后回捕调查
	mux.HandleFunc("POST /v1/surveys", func(w http.ResponseWriter, r *http.Request) {
		var in store.SurveyInput
		if !decodeJSON(w, r, &in) {
			return
		}
		sv, err := s.CreateSurvey(r.Context(), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, sv)
	})
	mux.HandleFunc("GET /v1/surveys/{id}", func(w http.ResponseWriter, r *http.Request) {
		sv, err := s.GetSurvey(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sv)
	})
	mux.HandleFunc("POST /v1/surveys/{id}/recaptures", func(w http.ResponseWriter, r *http.Request) {
		var in store.RecaptureInput
		if !decodeJSON(w, r, &in) {
			return
		}
		rc, err := s.AddRecapture(r.Context(), r.PathValue("id"), in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, rc)
	})
	mux.HandleFunc("GET /v1/recaptures", func(w http.ResponseWriter, r *http.Request) {
		recs, err := s.ListRecaptures(r.Context(), r.URL.Query().Get("lot_ref"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"recaptures": recs})
	})

	return mux
}
