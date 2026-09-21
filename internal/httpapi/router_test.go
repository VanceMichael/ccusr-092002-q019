package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

func newTestRouter(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "http.sqlite3"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return Router(s), s
}

func do(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, out
}

func TestHealth(t *testing.T) {
	h, _ := newTestRouter(t)
	code, body := do(t, h, http.MethodGet, "/health", nil)
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("健康接口异常: %d %v", code, body)
	}
}

func TestStagesList(t *testing.T) {
	h, _ := newTestRouter(t)
	code, body := do(t, h, http.MethodGet, "/v1/stages", nil)
	if code != http.StatusOK {
		t.Fatalf("环节列表状态码 %d", code)
	}
	stages, ok := body["stages"].([]any)
	if !ok || len(stages) != 6 {
		t.Fatalf("应有 6 个标准环节: %v", body["stages"])
	}
}

// TestAPIFullChainWithShortage HTTP 端到端：短少被 422/409 拦截，补确认死亡后放行直到放流与追溯。
func TestAPIFullChainWithShortage(t *testing.T) {
	h, _ := newTestRouter(t)

	code, body := do(t, h, "POST", "/v1/lots", map[string]any{
		"lot_ref": "LOT-HTTP-1", "species_code": "S_WANG", "origin": "WILD", "count": 50,
	})
	if code != http.StatusCreated {
		t.Fatalf("建批失败: %d %v", code, body)
	}

	// COLLECTION -> SORTING 正常。
	code, body = do(t, h, "POST", "/v1/handovers", map[string]any{
		"lot_ref": "LOT-HTTP-1", "from_stage": "COLLECTION", "to_stage": "SORTING",
		"sent_count": 50,
		"containers": []map[string]any{{"container_ref": "BX-1", "sent_count": 50}},
		"sent_by":    "集鱼站",
	})
	if code != http.StatusCreated {
		t.Fatalf("发出失败: %d %v", code, body)
	}
	ho1, _ := body["handover_id"].(string)
	code, body = do(t, h, "POST", "/v1/handovers/"+ho1+"/receive", map[string]any{
		"containers":  []map[string]any{{"container_ref": "BX-1", "received_count": 50}},
		"received_by": "分拣站",
	})
	if code != http.StatusOK || body["status"] != "CONFIRMED" {
		t.Fatalf("如数接收应 CONFIRMED: %d %v", code, body)
	}

	// SORTING -> RAIL_LIFT 短少 4 尾。
	_, body = do(t, h, "POST", "/v1/handovers", map[string]any{
		"lot_ref": "LOT-HTTP-1", "from_stage": "SORTING", "to_stage": "RAIL_LIFT",
		"sent_count": 50,
		"containers": []map[string]any{{"container_ref": "BX-2", "sent_count": 50}},
	})
	ho2, _ := body["handover_id"].(string)
	code, body = do(t, h, "POST", "/v1/handovers/"+ho2+"/receive", map[string]any{
		"received_count": 46, "received_by": "提升站",
	})
	if code != http.StatusOK || body["status"] != "DISPUTED" {
		t.Fatalf("短少应 DISPUTED: %d %v", code, body)
	}

	// 未销账试图继续发出 → 409。
	code, _ = do(t, h, "POST", "/v1/handovers", map[string]any{
		"lot_ref": "LOT-HTTP-1", "from_stage": "RAIL_LIFT", "to_stage": "LAND_TRANSPORT",
		"sent_count": 46,
		"containers": []map[string]any{{"container_ref": "BX-3", "sent_count": 46}},
	})
	if code != http.StatusConflict {
		t.Fatalf("批次未到提升段应 409，实际 %d", code)
	}

	// 登记并确认 4 尾死亡。
	_, body = do(t, h, "POST", "/v1/losses", map[string]any{
		"lot_ref": "LOT-HTTP-1", "handover_id": ho2, "count": 4,
		"cause": "轨道提升途中缺氧", "recorded_by": "提升站",
	})
	lossID, _ := body["loss_id"].(string)
	if lossID == "" {
		t.Fatalf("死亡登记失败: %v", body)
	}
	code, body = do(t, h, "POST", "/v1/losses/"+lossID+"/confirm", map[string]any{
		"confirmed_by": "环保监督员",
	})
	if code != http.StatusOK || body["status"] != "CONFIRMED" {
		t.Fatalf("死亡确认失败: %d %v", code, body)
	}

	// 销账查询应 cleared。
	code, body = do(t, h, "GET", "/v1/handovers/"+ho2+"/reconciliation", nil)
	if code != http.StatusOK || body["cleared"] != true {
		t.Fatalf("销账结果应 cleared: %d %v", code, body)
	}

	// 46 尾继续经过陆运、船运到放流点。
	for _, seg := range [][2]string{
		{"RAIL_LIFT", "LAND_TRANSPORT"},
		{"LAND_TRANSPORT", "VESSEL_TRANSPORT"},
		{"VESSEL_TRANSPORT", "RELEASE_SITE"},
	} {
		_, body = do(t, h, "POST", "/v1/handovers", map[string]any{
			"lot_ref": "LOT-HTTP-1", "from_stage": seg[0], "to_stage": seg[1],
			"sent_count": 46,
			"containers": []map[string]any{{"container_ref": "BX-" + seg[1], "sent_count": 46}},
		})
		ho, _ := body["handover_id"].(string)
		if code, b := do(t, h, "POST", "/v1/handovers/"+ho+"/receive", map[string]any{
			"received_count": 46,
		}); code != http.StatusOK || b["status"] != "CONFIRMED" {
			t.Fatalf("%s→%s 交接异常: %d %v", seg[0], seg[1], code, b)
		}
	}

	// 放流数量必须等于账面 46；误报 50 应被 422 拒绝。
	code, _ = do(t, h, "POST", "/v1/releases", map[string]any{
		"lot_ref": "LOT-HTTP-1", "count": 50,
		"waterbody": "金沙江", "site_name": "坝上放流点",
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("放流 50 ≠ 账面 46 应 422，实际 %d", code)
	}
	code, body = do(t, h, "POST", "/v1/releases", map[string]any{
		"lot_ref": "LOT-HTTP-1", "count": 46,
		"waterbody": "金沙江", "site_name": "坝上放流点",
		"latitude": 30.76, "longitude": 99.02,
		"released_at": "2026-05-10T11:00:00+08:00",
	})
	if code != http.StatusCreated {
		t.Fatalf("放流失败: %d %v", code, body)
	}

	// 追溯：守恒闭合、放流地点可见、问题清单为空。
	code, trace := do(t, h, "GET", "/v1/lots/LOT-HTTP-1/trace", nil)
	if code != http.StatusOK {
		t.Fatalf("追溯查询失败: %d", code)
	}
	if trace["conservation_ok"] != true {
		t.Fatalf("应守恒闭合: %v", trace["open_issues"])
	}
	stages, _ := trace["stages"].([]any)
	if len(stages) != 5 {
		t.Fatalf("应有 5 段交接记录，实际 %d", len(stages))
	}
	rel, _ := trace["release"].(map[string]any)
	if rel["waterbody"] != "金沙江" {
		t.Fatalf("放流地点缺失: %v", rel)
	}
}

// TestAPIRecaptureFlow 增殖批次标记 → 放流 → 调查回捕 → 追溯含监测证据。
func TestAPIRecaptureFlow(t *testing.T) {
	h, _ := newTestRouter(t)

	do(t, h, "POST", "/v1/lots", map[string]any{
		"lot_ref": "HATCH-9", "species_code": "S_KOZL", "origin": "HATCHERY", "count": 200,
	})
	_, body := do(t, h, "POST", "/v1/marks", map[string]any{
		"lot_ref": "HATCH-9", "mark_type": "OTOLITH", "mark_code": "OTO-BATCH-9",
		"marked_count": 200, "marked_at": "2026-03-01T09:00:00+08:00",
	})
	if body["mark_code"] != "OTO-BATCH-9" {
		t.Fatalf("标记登记异常: %v", body)
	}

	for _, seg := range [][2]string{
		{"COLLECTION", "SORTING"}, {"SORTING", "RAIL_LIFT"},
		{"RAIL_LIFT", "LAND_TRANSPORT"}, {"LAND_TRANSPORT", "VESSEL_TRANSPORT"},
		{"VESSEL_TRANSPORT", "RELEASE_SITE"},
	} {
		_, b := do(t, h, "POST", "/v1/handovers", map[string]any{
			"lot_ref": "HATCH-9", "from_stage": seg[0], "to_stage": seg[1],
			"sent_count": 200,
			"containers": []map[string]any{{"container_ref": "T-" + seg[1], "sent_count": 200}},
		})
		ho, _ := b["handover_id"].(string)
		if code, rb := do(t, h, "POST", "/v1/handovers/"+ho+"/receive", map[string]any{
			"received_count": 200,
		}); code != http.StatusOK || rb["status"] != "CONFIRMED" {
			t.Fatalf("%s→%s 异常: %d %v", seg[0], seg[1], code, rb)
		}
	}

	do(t, h, "POST", "/v1/releases", map[string]any{
		"lot_ref": "HATCH-9", "count": 200,
		"waterbody": "金沙江", "site_name": "叶巴滩坝上放流点",
		"released_at": "2026-03-05T10:00:00+08:00",
	})

	_, body = do(t, h, "POST", "/v1/surveys", map[string]any{
		"survey_ref": "MON-09", "survey_date": "2026-09-01T08:00:00+08:00",
		"waterbody": "金沙江", "site_name": "坝上15公里", "method": "电捕调查",
	})
	surveyID, _ := body["survey_id"].(string)
	_, body = do(t, h, "POST", "/v1/surveys/"+surveyID+"/recaptures", map[string]any{
		"mark_code": "OTO-BATCH-9", "species_code": "S_KOZL", "count": 7,
	})
	if body["match_status"] != "MATCHED" || body["matched_lot_ref"] != "HATCH-9" {
		t.Fatalf("回捕应匹配 HATCH-9: %v", body)
	}

	_, trace := do(t, h, "GET", "/v1/lots/HATCH-9/trace", nil)
	eff, _ := trace["effectiveness"].(map[string]any)
	if eff["eligible"] != true || eff["matched_recapture_count"].(float64) != 7 {
		t.Fatalf("成效统计异常: %v", eff)
	}
	recaps, _ := trace["recaptures"].([]any)
	if len(recaps) != 1 {
		t.Fatalf("追溯应含 1 条监测证据: %v", recaps)
	}
}

func TestAPIValidationErrors(t *testing.T) {
	h, _ := newTestRouter(t)

	// 非法 JSON。
	req := httptest.NewRequest("POST", "/v1/lots", bytes.NewReader([]byte("{bad json")))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，实际 %d", rec.Code)
	}

	// 未知字段。
	code, _ := do(t, h, "POST", "/v1/lots", map[string]any{
		"lot_ref": "X", "species_code": "S", "count": 1, "bogus": 1,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，实际 %d", code)
	}

	// 数量非法 → 422。
	code, body := do(t, h, "POST", "/v1/lots", map[string]any{
		"lot_ref": "X", "species_code": "S", "count": 0,
	})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("数量 0 应 422: %d %v", code, body)
	}

	// 查询不存在批次 → 404。
	code, _ = do(t, h, "GET", "/v1/lots/NOPE/trace", nil)
	if code != http.StatusNotFound {
		t.Fatalf("不存在批次应 404，实际 %d", code)
	}
}
