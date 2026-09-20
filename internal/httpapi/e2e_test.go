package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestYebatanFullChain 走通叶巴滩场景的完整故事：
// 增殖批次逐段交接、短数被阻断、死亡/重分拣凭证确认后才配平、
// 设备故障登记、放流落点、数月后耳石标记回捕计入成效而野生鱼不计入。
func TestYebatanFullChain(t *testing.T) {
	router := newTestRouter(t)
	do := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, router, method, path, body)
	}
	doCreated := func(method, path string, body any) map[string]any {
		t.Helper()
		status, decoded := do(method, path, body)
		if status != http.StatusCreated {
			t.Fatalf("%s %s 期望 201，实际 %d：%v", method, path, status, decoded)
		}
		return decoded
	}
	doOK := func(method, path string, body any) map[string]any {
		t.Helper()
		status, decoded := do(method, path, body)
		if status != http.StatusOK {
			t.Fatalf("%s %s 期望 200，实际 %d：%v", method, path, status, decoded)
		}
		return decoded
	}
	intAt := func(m map[string]any, keys ...string) int {
		t.Helper()
		current := m
		for i, key := range keys {
			value, ok := current[key]
			if !ok {
				t.Fatalf("响应缺少字段 %v：%v", keys, m)
			}
			if i == len(keys)-1 {
				number, ok := value.(float64)
				if !ok {
					t.Fatalf("字段 %v 不是数字：%v", keys, value)
				}
				return int(number)
			}
			next, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("字段 %s 不是对象：%v", key, value)
			}
			current = next
		}
		t.Fatal("unreachable")
		return 0
	}

	// 1. 登记一个增殖批次（120 尾）与一个野生批次（50 尾）。
	doCreated("POST", "/v1/lots", map[string]any{
		"lot_code": "LOT-A", "species_code": "SCHIZOTHORAX",
		"origin": "HATCHERY", "source": "叶巴滩增殖站",
		"initial_count": 120, "recorded_by": "分拣员甲",
	})
	doCreated("POST", "/v1/lots", map[string]any{
		"lot_code": "LOT-W", "species_code": "SCHIZOTHORAX",
		"origin": "WILD", "source": "坝下集鱼槽",
		"initial_count": 50, "recorded_by": "集鱼员乙",
	})
	// 重分拣并入的目标批次：0 尾从陆运段进入链路。
	doCreated("POST", "/v1/lots", map[string]any{
		"lot_code": "LOT-B", "species_code": "SCHIZOTHORAX",
		"origin": "HATCHERY", "source": "陆运段重新分拣",
		"initial_count": 0, "start_stage": "LAND_TRANSPORT",
	})

	// 2. 耳石标记只允许挂增殖批次；野生批次登记标记必须被拒绝。
	doCreated("POST", "/v1/lots/LOT-A/marks", map[string]any{
		"mark_type": "OTOLITH", "marker_code": "OT-2026-001",
		"marked_count": 120, "marked_at": "2026-04-01T09:00:00+08:00",
		"recorded_by": "增殖站丙",
	})
	if status, body := do("POST", "/v1/lots/LOT-W/marks", map[string]any{
		"mark_type": "OTOLITH", "marker_code": "OT-WILD-X",
		"marked_count": 1, "marked_at": "2026-04-01T09:00:00+08:00",
	}); status != http.StatusBadRequest {
		t.Fatalf("野生批次登记标记应返回 400，实际 %d：%v", status, body)
	}
	// 同一标记码不可重复登记。
	if status, _ := do("POST", "/v1/lots/LOT-A/marks", map[string]any{
		"mark_type": "OTOLITH", "marker_code": "OT-2026-001",
		"marked_count": 1, "marked_at": "2026-04-02T09:00:00+08:00",
	}); status != http.StatusConflict {
		t.Fatalf("重复标记应返回 409，实际 %d", status)
	}

	// 3. 分拣段发出 120 尾（两箱各 60），清单合计必须等于守恒应发数。
	doCreated("PUT", "/v1/lots/LOT-A/handoffs/SORTING", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "expected_count": 60},
			{"container_ref": "BOX-2", "expected_count": 60},
		},
		"reported_by": "分拣员甲",
	})
	if status, body := do("PUT", "/v1/lots/LOT-A/handoffs/SORTING", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "expected_count": 60},
		},
	}); status != http.StatusConflict {
		t.Fatalf("重复登记交接应 409，实际 %d：%v", status, body)
	}
	if status, body := do("PUT", "/v1/lots/LOT-A/handoffs/HOIST_155M", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-1", "expected_count": 120}},
	}); status != http.StatusConflict {
		t.Fatalf("上一段未接收就登记提升段应 409，实际 %d：%v", status, body)
	}
	// 运输箱合计与守恒应发数不符必须拒绝。
	// （先在野生批次上验证一次，避免干扰主批次状态。）
	if status, body := do("PUT", "/v1/lots/LOT-W/handoffs/SORTING", map[string]any{
		"containers": []map[string]any{{"container_ref": "W1", "expected_count": 49}},
	}); status != http.StatusBadRequest {
		t.Fatalf("箱量合计与应发数不符应 400，实际 %d：%v", status, body)
	}

	// 4. 分拣段逐箱清点 60/60，无短数即配平。
	sorting := doOK("POST", "/v1/lots/LOT-A/handoffs/SORTING/receive", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "received_count": 60},
			{"container_ref": "BOX-2", "received_count": 60},
		},
		"receiver_by": "提升站丁",
	})
	if sorting["status"] != "BALANCED" {
		t.Fatalf("分拣清点相符应为 BALANCED，实际 %v", sorting["status"])
	}
	if got := intAt(sorting, "expected_count"); got != 120 {
		t.Fatalf("分拣应发 120，实际 %d", got)
	}

	// 5. 155 米轨道提升：登记设备故障，随后清点短少 2 尾。
	doCreated("PUT", "/v1/lots/LOT-A/handoffs/HOIST_155M", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "expected_count": 60},
			{"container_ref": "BOX-2", "expected_count": 60},
		},
	})
	doCreated("POST", "/v1/lots/LOT-A/failures", map[string]any{
		"transfer_stage": "HOIST_155M", "equipment_code": "HOIST-01",
		"description": "轨道中段停机检修", "downtime_minutes": 35,
		"occurred_at": "2026-04-02T10:20:00+08:00", "reported_by": "提升站丁",
	})
	hoist := doOK("POST", "/v1/lots/LOT-A/handoffs/HOIST_155M/receive", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "received_count": 58},
			{"container_ref": "BOX-2", "received_count": 60},
		},
		"receiver_by": "无人车调度戊",
	})
	if hoist["status"] != "BLOCKED" || intAt(hoist, "shortage_count") != 2 {
		t.Fatalf("提升段短 2 尾应为 BLOCKED，实际 %v 短数 %v",
			hoist["status"], hoist["shortage_count"])
	}

	// 6. 短数未解释时陆运段不得继续流转。
	if status, body := do("PUT", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-1", "expected_count": 118}},
	}); status != http.StatusConflict {
		t.Fatalf("短数阻断期间应禁止下一段流转，实际 %d：%v", status, body)
	}

	// 7. 上报 2 尾死亡但尚未确认：仍然阻断。
	deathClaim := doCreated("POST", "/v1/lots/LOT-A/handoffs/HOIST_155M/losses", map[string]any{
		"kind": "DEATH", "count": 2, "reason": "提升挤压窒息",
		"evidence_ref": "EV-20260402-01", "reported_by": "提升站丁",
	})
	if deathClaim["status"] != "PENDING" {
		t.Fatalf("凭证初始应为 PENDING，实际 %v", deathClaim["status"])
	}
	if status, _ := do("PUT", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-1", "expected_count": 118}},
	}); status != http.StatusConflict {
		t.Fatalf("凭证未确认时仍应阻断，实际 %d", status)
	}
	// 凭证数量超过短数必须拒绝。
	if status, _ := do("POST", "/v1/lots/LOT-A/handoffs/HOIST_155M/losses", map[string]any{
		"kind": "DEATH", "count": 3,
	}); status != http.StatusConflict {
		t.Fatalf("凭证超出短数应 409，实际 %d", status)
	}
	// 确认死亡后提升段配平。
	deathID := intAt(deathClaim, "id")
	doOK("POST", path("/v1/loss-events/%d/confirm", deathID), map[string]any{
		"approve": true, "confirmed_by": "环保负责人己",
	})
	hoistAgain := doOK("GET", "/v1/lots/LOT-A/handoffs/HOIST_155M", nil)
	if hoistAgain["status"] != "BALANCED" || intAt(hoistAgain, "confirmed_loss") != 2 {
		t.Fatalf("死亡确认后应配平，实际 %v", hoistAgain["status"])
	}

	// 8. 陆运段应发 118 尾（守恒自动承接上一段实收），清点又短 2 尾，
	//    本次确认为重新分拣并入 LOT-B。
	doCreated("PUT", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "expected_count": 58},
			{"container_ref": "BOX-2", "expected_count": 60},
		},
	})
	land := doOK("POST", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT/receive", map[string]any{
		"containers": []map[string]any{
			{"container_ref": "BOX-1", "received_count": 56},
			{"container_ref": "BOX-2", "received_count": 60},
		},
	})
	if land["status"] != "BLOCKED" || intAt(land, "shortage_count") != 2 {
		t.Fatalf("陆运段应短 2 尾并 BLOCKED，实际 %v", land["status"])
	}
	// 重分拣必须指定并入批次。
	if status, _ := do("POST", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT/losses", map[string]any{
		"kind": "RESORT", "count": 2,
	}); status != http.StatusBadRequest {
		t.Fatalf("重分拣缺少目标批次应 400，实际 %d", status)
	}
	resort := doCreated("POST", "/v1/lots/LOT-A/handoffs/LAND_TRANSPORT/losses", map[string]any{
		"kind": "RESORT", "count": 2, "to_lot_code": "LOT-B",
		"reason": "混装中检出另一种规格，并入 LOT-B", "reported_by": "无人车调度戊",
	})
	resortID := intAt(resort, "id")
	// 未确认仍阻断船运。
	if status, _ := do("PUT", "/v1/lots/LOT-A/handoffs/VESSEL_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-2", "expected_count": 116}},
	}); status != http.StatusConflict {
		t.Fatalf("重分拣未确认应阻断船运，实际 %d", status)
	}
	doOK("POST", path("/v1/loss-events/%d/confirm", resortID), map[string]any{
		"approve": true, "confirmed_by": "环保负责人己",
	})

	// 9. 船运 116 尾、放流 116 尾，配平后必须登记放流地点才能完成放流。
	doCreated("PUT", "/v1/lots/LOT-A/handoffs/VESSEL_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-2", "expected_count": 116}},
	})
	doOK("POST", "/v1/lots/LOT-A/handoffs/VESSEL_TRANSPORT/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-2", "received_count": 116}},
	})
	doCreated("PUT", "/v1/lots/LOT-A/handoffs/RELEASE", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-2", "expected_count": 116}},
	})
	doOK("POST", "/v1/lots/LOT-A/handoffs/RELEASE/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "BOX-2", "received_count": 116}},
	})
	// 未到配平不放流；此处已配平，但地点缺失由参数校验拦截。
	if status, body := do("POST", "/v1/lots/LOT-A/release", map[string]any{
		"site_name": "坝上缓流区",
	}); status != http.StatusBadRequest {
		t.Fatalf("缺少 site_code 应 400，实际 %d：%v", status, body)
	}
	lon, lat := 98.9123, 30.7654
	doOK("POST", "/v1/lots/LOT-A/release", map[string]any{
		"site_code": "REL-UPPER-01", "site_name": "坝上缓流放流区",
		"longitude": lon, "latitude": lat,
		"released_at": "2026-04-03T11:30:00+08:00",
	})

	// 10. 被并入的 LOT-B 从陆运段入链，应发基数自动为 2，箱量不符时先拒绝一次。
	if status, _ := do("PUT", "/v1/lots/LOT-B/handoffs/LAND_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "expected_count": 1}},
	}); status != http.StatusBadRequest {
		t.Fatalf("LOT-B 应发 2 尾，报 1 应 400，实际 %d", status)
	}
	doCreated("PUT", "/v1/lots/LOT-B/handoffs/LAND_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "expected_count": 2}},
	})
	doOK("POST", "/v1/lots/LOT-B/handoffs/LAND_TRANSPORT/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "received_count": 2}},
	})
	doCreated("PUT", "/v1/lots/LOT-B/handoffs/VESSEL_TRANSPORT", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "expected_count": 2}},
	})
	doOK("POST", "/v1/lots/LOT-B/handoffs/VESSEL_TRANSPORT/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "received_count": 2}},
	})
	doCreated("PUT", "/v1/lots/LOT-B/handoffs/RELEASE", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "expected_count": 2}},
	})
	doOK("POST", "/v1/lots/LOT-B/handoffs/RELEASE/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "B1", "received_count": 2}},
	})
	doOK("POST", "/v1/lots/LOT-B/release", map[string]any{
		"site_code": "REL-UPPER-02", "site_name": "支流交汇放流区",
		"released_at": "2026-04-03T12:00:00+08:00",
	})

	// 11. 野生批次：短数凭证先被驳回，仍阻断；重新上报并确认后才配平。
	doCreated("PUT", "/v1/lots/LOT-W/handoffs/SORTING", map[string]any{
		"containers": []map[string]any{{"container_ref": "W1", "expected_count": 50}},
	})
	wildSorting := doOK("POST", "/v1/lots/LOT-W/handoffs/SORTING/receive", map[string]any{
		"containers": []map[string]any{{"container_ref": "W1", "received_count": 49}},
	})
	if wildSorting["status"] != "BLOCKED" {
		t.Fatalf("野生批次短 1 尾也应 BLOCKED，实际 %v", wildSorting["status"])
	}
	rejected := doCreated("POST", "/v1/lots/LOT-W/handoffs/SORTING/losses", map[string]any{
		"kind": "DEATH", "count": 1, "evidence_ref": "EV-BAD",
	})
	rejectedID := intAt(rejected, "id")
	doOK("POST", path("/v1/loss-events/%d/confirm", rejectedID), map[string]any{
		"approve": false, "confirmed_by": "环保负责人己", "note": "证据不足，驳回",
	})
	stillBlocked := doOK("GET", "/v1/lots/LOT-W/handoffs/SORTING", nil)
	if stillBlocked["status"] != "BLOCKED" {
		t.Fatalf("凭证驳回后应仍 BLOCKED，实际 %v", stillBlocked["status"])
	}
	again := doCreated("POST", "/v1/lots/LOT-W/handoffs/SORTING/losses", map[string]any{
		"kind": "DEATH", "count": 1, "evidence_ref": "EV-GOOD",
	})
	doOK("POST", path("/v1/loss-events/%d/confirm", intAt(again, "id")), map[string]any{
		"approve": true, "confirmed_by": "环保负责人己",
	})

	// 12. 数月后的回捕调查：命中耳石标记计成效，无标记野生鱼与有标无录分账展示。
	doCreated("POST", "/v1/surveys", map[string]any{
		"survey_code": "SVY-2027-03", "survey_date": "2027-03-15",
		"site_code": "MON-DN-01", "site_name": "坝下 20 公里监测断面",
		"method": "刺网+地笼", "investigators": "监测队庚",
	})
	credited := doCreated("POST", "/v1/surveys/SVY-2027-03/recaptures", map[string]any{
		"species_code": "SCHIZOTHORAX", "count": 3,
		"mark_type": "OTOLITH", "marker_code": "OT-2026-001",
		"evidence_ref": "OTOSAMPLE-1",
	})
	if credited["origin_result"] != "CREDITED" || credited["matched_lot_code"] != "LOT-A" {
		t.Fatalf("耳石命中应 CREDITED 并关联 LOT-A，实际 %v", credited)
	}
	wildCatch := doCreated("POST", "/v1/surveys/SVY-2027-03/recaptures", map[string]any{
		"species_code": "SCHIZOTHORAX", "count": 7,
	})
	if wildCatch["origin_result"] != "WILD" {
		t.Fatalf("无标记鱼应定性 WILD，实际 %v", wildCatch["origin_result"])
	}
	unmatched := doCreated("POST", "/v1/surveys/SVY-2027-03/recaptures", map[string]any{
		"species_code": "SCHIZOTHORAX", "count": 1,
		"mark_type": "FLUORESCENT", "marker_code": "FL-UNKNOWN",
	})
	if unmatched["origin_result"] != "UNMATCHED_MARK" {
		t.Fatalf("查无此标记应 UNMATCHED_MARK，实际 %v", unmatched["origin_result"])
	}

	// 13. 管理部门查询 LOT-A 全链路：守恒账、每次交接、放流点、监测证据一眼可见。
	trace := doOK("GET", "/v1/lots/LOT-A/trace", nil)
	conservation, ok := trace["conservation"].(map[string]any)
	if !ok {
		t.Fatalf("trace 缺少 conservation：%v", trace)
	}
	checks := map[string]int{
		"initial_count":    120,
		"resort_out":       2,
		"confirmed_deaths": 2,
		"accounted_count":  116,
		"released_count":   116,
	}
	for field, want := range checks {
		if got := int(conservation[field].(float64)); got != want {
			t.Fatalf("守恒账 %s 期望 %d，实际 %d", field, want, got)
		}
	}
	if conservation["balanced"] != true {
		t.Fatalf("全链路配平后 balanced 应为 true，实际 %v", conservation["balanced"])
	}
	handoffs := trace["handoffs"].([]any)
	if len(handoffs) != 5 {
		t.Fatalf("应有 5 段交接，实际 %d", len(handoffs))
	}
	wantStages := []string{"SORTING", "HOIST_155M", "LAND_TRANSPORT", "VESSEL_TRANSPORT", "RELEASE"}
	for i, h := range handoffs {
		hm := h.(map[string]any)
		if hm["stage_code"] != wantStages[i] || hm["status"] != "BALANCED" {
			t.Fatalf("第 %d 段应为 %s/BALANCED，实际 %v/%v",
				i+1, wantStages[i], hm["stage_code"], hm["status"])
		}
	}
	release, ok := trace["release"].(map[string]any)
	if !ok || release["site_code"] != "REL-UPPER-01" || int(release["count"].(float64)) != 116 {
		t.Fatalf("放流汇总不正确：%v", trace["release"])
	}
	failures := trace["equipment_failures"].([]any)
	if len(failures) != 1 || failures[0].(map[string]any)["equipment_code"] != "HOIST-01" {
		t.Fatalf("应查到提升机故障 1 条，实际 %v", failures)
	}
	monitoring := trace["monitoring"].(map[string]any)
	if int(monitoring["credited_recaptures"].(float64)) != 3 {
		t.Fatalf("放流成效应只计命中标记的 3 尾，实际 %v", monitoring["credited_recaptures"])
	}
	if int(monitoring["wild_recaptures"].(float64)) != 7 {
		t.Fatalf("并列的野生鱼应为 7 尾，实际 %v", monitoring["wild_recaptures"])
	}
	if int(monitoring["unmatched_marks"].(float64)) != 1 {
		t.Fatalf("有标无录应为 1 尾，实际 %v", monitoring["unmatched_marks"])
	}
	surveys := monitoring["surveys"].([]any)
	if len(surveys) != 1 || surveys[0].(map[string]any)["survey_code"] != "SVY-2027-03" {
		t.Fatalf("监测证据应关联 1 次调查，实际 %v", surveys)
	}

	// 14. 野生批次的 trace：无标记、放流成效为 0，天然鱼绝不被误计。
	wildTrace := doOK("GET", "/v1/lots/LOT-W/trace", nil)
	if marks := wildTrace["marks"].([]any); len(marks) != 0 {
		t.Fatalf("野生批次不应有增殖标记，实际 %v", marks)
	}
	wildMon := wildTrace["monitoring"].(map[string]any)
	if int(wildMon["credited_recaptures"].(float64)) != 0 {
		t.Fatalf("野生批次放流成效必须为 0，实际 %v", wildMon["credited_recaptures"])
	}

	// 15. 基础查询语义：未知批次 404；列表可按来源过滤。
	if status, _ := do("GET", "/v1/lots/NOPE/trace", nil); status != http.StatusNotFound {
		t.Fatalf("未知批次 trace 应 404，实际 %d", status)
	}
	status, body := do("GET", "/v1/lots?origin=HATCHERY", nil)
	if status != http.StatusOK || len(body["lots"].([]any)) != 2 {
		t.Fatalf("增殖批次应有 2 个，实际 %d", status)
	}
}

func doJSON(t *testing.T, h http.Handler, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("编码请求失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	decoded := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("响应不是 JSON（%d）: %v", rec.Code, rec.Body.String())
		}
	}
	return rec.Code, decoded
}

func path(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
