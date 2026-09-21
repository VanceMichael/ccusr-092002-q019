package store

import (
	"context"
	"strings"
	"testing"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// TestResortAtSorting 分拣环节物种复核：把 5 尾划入新批次，两批守恒分别闭合。
func TestResortAtSorting(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-R1", domain.OriginWild, 100)
	transfer(t, ctx, s, "LOT-R1", "COLLECTION", "SORTING", 100)

	ev, err := s.RegisterResort(ctx, ResortInput{
		FromLotRef: "LOT-R1", ToSpecies: "S_DOLI", StageCode: "SORTING",
		Count: 5, Reason: "物种复核：5 尾为长丝裂腹鱼", RecordedBy: "分拣员",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status != "PENDING" {
		t.Fatalf("重分拣初始应为 PENDING，实际 %s", ev.Status)
	}
	// 未确认前鱼仍在原批次账面，且原批次不得继续流转。
	if _, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-R1", FromStage: "SORTING", ToStage: "RAIL_LIFT",
		SentCount:  100,
		Containers: []ContainerCount{{ContainerRef: "C", SentCount: 100}},
	}); err == nil || !strings.Contains(err.Error(), "重新分拣") {
		t.Fatalf("待确认重分拣应阻止流转，实际: %v", err)
	}
	if _, err := s.ConfirmResort(ctx, ev.ResortID, "值班主管"); err != nil {
		t.Fatal(err)
	}

	b1, _ := s.Balance(ctx, "LOT-R1")
	if b1.Available != 95 || b1.ResortedOut != 5 {
		t.Fatalf("原批次应为 95（划出5），实际 %+v", b1)
	}
	toLot, err := s.GetLot(ctx, ev.ToLotRef)
	if err != nil {
		t.Fatal(err)
	}
	if toLot.SpeciesCode != "S_DOLI" || toLot.Origin != domain.OriginWild {
		t.Fatalf("新批次物种/来源异常: %+v", toLot)
	}
	b2, _ := s.Balance(ctx, toLot.LotRef)
	if b2.Available != 5 || b2.ResortedIn != 5 {
		t.Fatalf("新批次应为划入 5，实际 %+v", b2)
	}

	// 两个批次分别走完链路并放流。
	for _, ref := range []string{"LOT-R1", toLot.LotRef} {
		n := 95
		if ref == toLot.LotRef {
			n = 5
		}
		transfer(t, ctx, s, ref, "SORTING", "RAIL_LIFT", n)
		transfer(t, ctx, s, ref, "RAIL_LIFT", "LAND_TRANSPORT", n)
		transfer(t, ctx, s, ref, "LAND_TRANSPORT", "VESSEL_TRANSPORT", n)
		transfer(t, ctx, s, ref, "VESSEL_TRANSPORT", "RELEASE_SITE", n)
		if _, err := s.CreateRelease(ctx, ReleaseInput{
			LotRef: ref, Count: n, Waterbody: "金沙江", SiteName: "坝上放流点",
		}); err != nil {
			t.Fatalf("批次 %s 放流失败: %v", ref, err)
		}
	}
	for _, ref := range []string{"LOT-R1", toLot.LotRef} {
		trace, err := s.TraceLot(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if !trace.ConservationOK {
			t.Fatalf("批次 %s 应守恒: %v", ref, trace.OpenIssues)
		}
	}
}

// TestResortClearsShortage 交接短少由经确认的重分拣记录销账（鱼没有死，是分拣错批）。
func TestResortClearsShortage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-S1", domain.OriginWild, 50)
	createLot(t, ctx, s, "LOT-S2", domain.OriginWild, 50)
	// 两批先到分拣段；S2 先一步随箱体提升到 RAIL_LIFT，S1 在提升接收时发现短少。
	transfer(t, ctx, s, "LOT-S2", "COLLECTION", "SORTING", 50)
	transfer(t, ctx, s, "LOT-S1", "COLLECTION", "SORTING", 50)
	transfer(t, ctx, s, "LOT-S2", "SORTING", "RAIL_LIFT", 50)

	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-S1", FromStage: "SORTING", ToStage: "RAIL_LIFT", SentCount: 50,
		Containers: []ContainerCount{{ContainerRef: "C", SentCount: 50}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, ReceiveInput{HandoverID: h.HandoverID, ReceivedCount: 48}); err != nil {
		t.Fatal(err)
	}
	// 2 尾复核发现属于 LOT-S2（同处分拣段）。
	ev, err := s.RegisterResort(ctx, ResortInput{
		FromLotRef: "LOT-S1", ToLotRef: "LOT-S2", HandoverID: h.HandoverID,
		Count: 2, Reason: "短少复核：2 尾在分拣时误入 S2 箱体",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmResort(ctx, ev.ResortID, "主管"); err != nil {
		t.Fatal(err)
	}
	h2, err := s.GetHandover(ctx, h.HandoverID)
	if err != nil {
		t.Fatal(err)
	}
	if h2.Status != "CONFIRMED" {
		t.Fatalf("重分拣确认后交接应 CONFIRMED，实际 %s", h2.Status)
	}
	b1, _ := s.Balance(ctx, "LOT-S1")
	b2, _ := s.Balance(ctx, "LOT-S2")
	if b1.Available != 48 || b2.Available != 52 {
		t.Fatalf("销账后应为 S1=48 S2=52，实际 %d/%d", b1.Available, b2.Available)
	}
}

// TestRejectedLossReopens 驳回的死亡记录不能销账，批次继续卡住。
func TestRejectedLossReopens(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-X", domain.OriginWild, 20)
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-X", FromStage: "COLLECTION", ToStage: "SORTING", SentCount: 20,
		Containers: []ContainerCount{{ContainerRef: "C", SentCount: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 第一段如数接收。
	if _, err := s.Receive(ctx, ReceiveInput{
		HandoverID: h.HandoverID,
		Received:   []ContainerCount{{ContainerRef: "C", ReceivedCount: 20}},
	}); err != nil {
		t.Fatal(err)
	}
	// 站点登记 1 尾死亡。
	loss, err := s.RegisterLoss(ctx, LossInput{
		LotRef: "LOT-X", StageCode: "SORTING", Count: 1, Cause: "擦伤", RecordedBy: "分拣员",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectLoss(ctx, loss.LossID, "主管", "复核仍存活，计数差错"); err != nil {
		t.Fatal(err)
	}
	bal, _ := s.Balance(ctx, "LOT-X")
	if bal.Available != 20 {
		t.Fatalf("驳回死亡后账面应仍为 20，实际 %d", bal.Available)
	}
	// 驳回后批次应可正常流转。
	transfer(t, ctx, s, "LOT-X", "SORTING", "RAIL_LIFT", 20)
}

// TestResortIntoReleasedTargetRejected 目标批次在重分拣登记后、确认前放流的，确认必须被拒。
func TestResortIntoReleasedTargetRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-A1", domain.OriginHatchery, 30)
	createLot(t, ctx, s, "LOT-A2", domain.OriginHatchery, 30)
	transfer(t, ctx, s, "LOT-A1", "COLLECTION", "SORTING", 30)
	transfer(t, ctx, s, "LOT-A2", "COLLECTION", "SORTING", 30)

	ev, err := s.RegisterResort(ctx, ResortInput{
		FromLotRef: "LOT-A1", ToLotRef: "LOT-A2", StageCode: "SORTING",
		Count: 2, Reason: "物种复核待确认",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 目标批次继续走完链路并放流（来源批次的待确认重分拣不影响目标）。
	transfer(t, ctx, s, "LOT-A2", "SORTING", "RAIL_LIFT", 30)
	transfer(t, ctx, s, "LOT-A2", "RAIL_LIFT", "LAND_TRANSPORT", 30)
	transfer(t, ctx, s, "LOT-A2", "LAND_TRANSPORT", "VESSEL_TRANSPORT", 30)
	transfer(t, ctx, s, "LOT-A2", "VESSEL_TRANSPORT", "RELEASE_SITE", 30)
	if _, err := s.CreateRelease(ctx, ReleaseInput{
		LotRef: "LOT-A2", Count: 30, Waterbody: "金沙江", SiteName: "坝上放流点",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmResort(ctx, ev.ResortID, "主管"); err == nil {
		t.Fatal("向已放流目标批次划入鱼必须被拒绝")
	}
}

// 无标记野生鱼回捕不计入成效。
func TestHatcheryMarksAndRecaptureEffectiveness(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 增殖批次：1000 尾，荧光+耳石双重批次标记，走完全链路放流。
	createLot(t, ctx, s, "HATCH-1", domain.OriginHatchery, 1000)
	mk1, err := s.AddMark(ctx, MarkInput{
		LotRef: "HATCH-1", MarkType: "FLUORESCENT", MarkCode: "FLUO-YBT-2026-01",
		MarkedCount: 1000, MarkedAt: "2026-04-01T09:00:00+08:00",
		Method: "茜素红S浸泡", Notes: "增殖站标记批次",
	})
	if err != nil {
		t.Fatal(err)
	}
	mk2, err := s.AddMark(ctx, MarkInput{
		LotRef: "HATCH-1", MarkType: "OTOLITH", MarkCode: "OTO-YBT-2026-01",
		MarkedCount: 1000, MarkedAt: "2026-04-01T09:30:00+08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = mk2
	stages := domain.OrderedStages
	for i := 0; i+1 < len(stages); i++ {
		transfer(t, ctx, s, "HATCH-1", string(stages[i]), string(stages[i+1]), 1000)
	}
	if _, err := s.CreateRelease(ctx, ReleaseInput{
		LotRef: "HATCH-1", Count: 1000, Waterbody: "金沙江", SiteName: "叶巴滩坝上放流点",
		ReleasedAt: "2026-04-05T10:00:00+08:00",
	}); err != nil {
		t.Fatal(err)
	}

	// 野生过坝鱼批次同样放流，无标记。
	createLot(t, ctx, s, "WILD-1", domain.OriginWild, 300)
	for i := 0; i+1 < len(stages); i++ {
		transfer(t, ctx, s, "WILD-1", string(stages[i]), string(stages[i+1]), 300)
	}
	if _, err := s.CreateRelease(ctx, ReleaseInput{
		LotRef: "WILD-1", Count: 300, Waterbody: "金沙江", SiteName: "叶巴滩坝上放流点",
		ReleasedAt: "2026-04-06T10:00:00+08:00",
	}); err != nil {
		t.Fatal(err)
	}

	// 三个月后的回捕调查。
	sv, err := s.CreateSurvey(ctx, SurveyInput{
		SurveyRef: "YBT-MON-2026-07", SurveyDate: "2026-07-18T08:00:00+08:00",
		Waterbody: "金沙江", SiteName: "坝上10公里江段", Method: "刺网+地笼",
		InvestigatorRef: "水生所调查组",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 12 尾检出荧光标记 → 命中增殖批次。
	rc1, err := s.AddRecapture(ctx, sv.SurveyID, RecaptureInput{
		MarkCode: mk1.MarkCode, SpeciesCode: "S_WANG", Count: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rc1.MatchStatus != "MATCHED" || rc1.MatchedLotRef != "HATCH-1" {
		t.Fatalf("标记应匹配 HATCH-1: %+v", rc1)
	}
	// 80 尾无标记野生鱼 → UNMATCHED，不归属任何批次。
	rc2, err := s.AddRecapture(ctx, sv.SurveyID, RecaptureInput{
		SpeciesCode: "S_WANG", Count: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rc2.MatchStatus != "UNMATCHED" || rc2.MatchedLotRef != "" {
		t.Fatalf("无标记回捕必须 UNMATCHED: %+v", rc2)
	}
	// 1 尾标记编码查无此批 → UNMATCHED。
	rc3, err := s.AddRecapture(ctx, sv.SurveyID, RecaptureInput{
		MarkCode: "FLUO-UNKNOWN-99", SpeciesCode: "S_WANG", Count: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rc3.MatchStatus != "UNMATCHED" {
		t.Fatalf("未知标记编码必须 UNMATCHED: %+v", rc3)
	}

	// 增殖批次追溯：成效只算命中的 12 尾。
	trace, err := s.TraceLot(ctx, "HATCH-1")
	if err != nil {
		t.Fatal(err)
	}
	if !trace.Effectiveness.Eligible {
		t.Fatal("HATCHERY 批次应可计入放流成效")
	}
	if trace.Effectiveness.MatchedRecaptureCount != 12 || trace.Effectiveness.SurveyCount != 1 {
		t.Fatalf("成效口径应只统计命中 12 尾/1 次调查: %+v", trace.Effectiveness)
	}
	if len(trace.Recaptures) != 1 || trace.Recaptures[0].SurveyDate == "" {
		t.Fatalf("追溯应带出监测证据（调查日期/水体）: %+v", trace.Recaptures)
	}

	// 野生批次追溯：明确不可计入成效，且不携带命中回捕。
	wtrace, err := s.TraceLot(ctx, "WILD-1")
	if err != nil {
		t.Fatal(err)
	}
	if wtrace.Effectiveness.Eligible || wtrace.Effectiveness.MatchedRecaptureCount != 0 {
		t.Fatalf("野生鱼不得计入放流成效: %+v", wtrace.Effectiveness)
	}
}
