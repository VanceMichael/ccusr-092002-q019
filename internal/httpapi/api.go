package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// API 汇总 HTTP 处理器对领域服务的依赖。
type API struct {
	service *domain.Service
}

// NewAPI 创建绑定领域服务的 API。
func NewAPI(service *domain.Service) *API {
	return &API{service: service}
}

// Router 装配全部路由。
func (a *API) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)

	mux.HandleFunc("POST /v1/lots", a.createLot)
	mux.HandleFunc("GET /v1/lots", a.listLots)
	mux.HandleFunc("GET /v1/lots/{lot}", a.getLot)
	mux.HandleFunc("GET /v1/lots/{lot}/trace", a.traceLot)

	mux.HandleFunc("PUT /v1/lots/{lot}/handoffs/{stage}", a.dispatch)
	mux.HandleFunc("POST /v1/lots/{lot}/handoffs/{stage}/receive", a.receive)
	mux.HandleFunc("GET /v1/lots/{lot}/handoffs/{stage}", a.getHandoff)

	mux.HandleFunc("POST /v1/lots/{lot}/handoffs/{stage}/losses", a.reportLoss)
	mux.HandleFunc("GET /v1/lots/{lot}/losses", a.listLosses)
	mux.HandleFunc("POST /v1/loss-events/{id}/confirm", a.confirmLoss)

	mux.HandleFunc("POST /v1/lots/{lot}/release", a.completeRelease)

	mux.HandleFunc("POST /v1/lots/{lot}/failures", a.registerLotFailure)
	mux.HandleFunc("GET /v1/lots/{lot}/failures", a.listLotFailures)
	mux.HandleFunc("POST /v1/failures", a.registerFailure)

	mux.HandleFunc("POST /v1/lots/{lot}/marks", a.registerMark)
	mux.HandleFunc("GET /v1/lots/{lot}/marks", a.listMarks)

	mux.HandleFunc("POST /v1/surveys", a.createSurvey)
	mux.HandleFunc("GET /v1/surveys/{survey}", a.getSurvey)
	mux.HandleFunc("POST /v1/surveys/{survey}/recaptures", a.addRecapture)
	mux.HandleFunc("GET /v1/surveys/{survey}/recaptures", a.listRecaptures)

	return loggingMiddleware(mux)
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) createLot(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateLotRequest
	if !decode(w, r, &req) {
		return
	}
	lot, err := a.service.CreateLot(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, lot)
}

func (a *API) listLots(w http.ResponseWriter, r *http.Request) {
	lots, err := a.service.ListLots(r.Context(),
		r.URL.Query().Get("origin"), r.URL.Query().Get("species"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lots": orEmpty(lots)})
}

func (a *API) getLot(w http.ResponseWriter, r *http.Request) {
	lot, err := a.service.GetLot(r.Context(), r.PathValue("lot"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lot)
}

func (a *API) traceLot(w http.ResponseWriter, r *http.Request) {
	trace, err := a.service.Trace(r.Context(), r.PathValue("lot"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (a *API) dispatch(w http.ResponseWriter, r *http.Request) {
	var req domain.DispatchRequest
	if !decode(w, r, &req) {
		return
	}
	// 路径中的环节优先；未在路径给出时取请求体 transfer_stage。
	if stage := r.PathValue("stage"); stage != "" {
		req.StageCode = stage
	}
	view, err := a.service.Dispatch(r.Context(), r.PathValue("lot"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) receive(w http.ResponseWriter, r *http.Request) {
	var req domain.ReceiveRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.Receive(r.Context(),
		r.PathValue("lot"), r.PathValue("stage"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) getHandoff(w http.ResponseWriter, r *http.Request) {
	view, err := a.service.GetHandoff(r.Context(),
		r.PathValue("lot"), r.PathValue("stage"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) reportLoss(w http.ResponseWriter, r *http.Request) {
	var req domain.LossRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.ReportLoss(r.Context(),
		r.PathValue("lot"), r.PathValue("stage"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) listLosses(w http.ResponseWriter, r *http.Request) {
	views, err := a.service.ListLossEvents(r.Context(), r.PathValue("lot"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loss_events": orEmpty(views)})
}

func (a *API) confirmLoss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Code: "bad_request",
			Message: "凭证 id 必须为整数"})
		return
	}
	var req domain.ConfirmClaimRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.ConfirmLossEvent(r.Context(), id, req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) completeRelease(w http.ResponseWriter, r *http.Request) {
	var req domain.CompleteReleaseRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.CompleteRelease(r.Context(), r.PathValue("lot"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) registerLotFailure(w http.ResponseWriter, r *http.Request) {
	var req domain.FailureRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.RegisterFailure(r.Context(), r.PathValue("lot"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) listLotFailures(w http.ResponseWriter, r *http.Request) {
	views, err := a.service.ListFailures(r.Context(), r.PathValue("lot"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"equipment_failures": orEmpty(views)})
}

func (a *API) registerFailure(w http.ResponseWriter, r *http.Request) {
	var req domain.FailureRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.RegisterFailure(r.Context(), "", req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) registerMark(w http.ResponseWriter, r *http.Request) {
	var req domain.MarkRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.RegisterMark(r.Context(), r.PathValue("lot"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) listMarks(w http.ResponseWriter, r *http.Request) {
	views, err := a.service.ListMarks(r.Context(), r.PathValue("lot"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"marks": orEmpty(views)})
}

func (a *API) createSurvey(w http.ResponseWriter, r *http.Request) {
	var req domain.SurveyRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.CreateSurvey(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) getSurvey(w http.ResponseWriter, r *http.Request) {
	view, err := a.service.GetSurvey(r.Context(), r.PathValue("survey"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) addRecapture(w http.ResponseWriter, r *http.Request) {
	var req domain.RecaptureRequest
	if !decode(w, r, &req) {
		return
	}
	view, err := a.service.AddRecapture(r.Context(), r.PathValue("survey"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *API) listRecaptures(w http.ResponseWriter, r *http.Request) {
	views, err := a.service.ListRecaptures(r.Context(), r.PathValue("survey"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recaptures": orEmpty(views)})
}

// --- 编解码与错误映射 ---

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Code: "bad_request", Message: "请求体不是合法 JSON：" + cleanJSONError(err),
		})
		return false
	}
	return true
}

func cleanJSONError(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "json: ")
	return msg
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	body := errorBody{Message: err.Error()}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
		body.Code = "not_found"
	case errors.Is(err, domain.ErrValidation), errors.Is(err, domain.ErrWildMark):
		status = http.StatusBadRequest
		body.Code = "validation_failed"
	case errors.Is(err, domain.ErrAlreadyExists):
		status = http.StatusConflict
		body.Code = "already_exists"
	case errors.Is(err, domain.ErrConflict),
		errors.Is(err, domain.ErrStageOutOfOrder),
		errors.Is(err, domain.ErrPriorUnbalanced),
		errors.Is(err, domain.ErrClaimInsufficient),
		errors.Is(err, domain.ErrClaimOvercount),
		errors.Is(err, domain.ErrReleaseRequiresBalance):
		status = http.StatusConflict
		body.Code = "state_conflict"
	default:
		body.Code = "internal_error"
		body.Message = "服务器内部错误"
		log.Printf("未处理错误: %v", err)
	}
	writeJSON(w, status, body)
}

// orEmpty 保证切片序列化为 [] 而非 null。
func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
