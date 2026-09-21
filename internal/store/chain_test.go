package store

import (
	"context"
	"strings"
	"testing"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// transfer 完成一段无短少交接：整批发出并按箱如数接收。
func transfer(t *testing.T, ctx context.Context, s *Store, lotRef, from, to string, n int) *Handover {
	t.Helper()
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef:     lotRef,
		FromStage:  from,
		ToStage:    to,
		SentCount:  n,
		Containers: []ContainerCount{{ContainerRef: "BOX-" + to, SentCount: n}},
		SentBy:     "上游值班员",
	})
	if err != nil {
		t.Fatalf("发出 %s→%s 失败: %v", from, to, err)
	}
	h, err = s.Receive(ctx, ReceiveInput{
		HandoverID: h.HandoverID,
		Received:   []ContainerCount{{ContainerRef: "BOX-" + to, ReceivedCount: n}},
		ReceivedBy: "下游值班员",
	})
	if err != nil {
		t.Fatalf("接收 %s→%s 失败: %v", from, to, err)
	}
	return h
}

func createLot(t *testing.T, ctx context.Context, s *Store, ref, origin string, n int) *Lot {
	t.Helper()
	lot, err := s.CreateLot(ctx, CreateLotInput{
		LotRef: ref, SpeciesCode: "S_WANG", Origin: origin, Count: n,
	})
	if err != nil {
		t.Fatalf("创建批次失败: %v", err)
	}
	return lot
}

// TestHappyPathFullChain 正常批次走完集鱼槽→分拣→提升→陆运→船运→放流点并放流，全程守恒。
func TestHappyPathFullChain(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-A", domain.OriginWild, 120)

	stages := domain.OrderedStages
	for i := 0; i+1 < len(stages); i++ {
		transfer(t, ctx, s, "LOT-A", string(stages[i]), string(stages[i+1]), 120)
	}

	lot, err := s.GetLot(ctx, "LOT-A")
	if err != nil {
		t.Fatal(err)
	}
	if lot.CurrentStage != "RELEASE_SITE" || lot.Status != "IN_CHAIN" {
		t.Fatalf("批次应已到放流点且可流转，实际 stage=%s status=%s", lot.CurrentStage, lot.Status)
	}

	rel, err := s.CreateRelease(ctx, ReleaseInput{
		LotRef: "LOT-A", Count: 120, Waterbody: "金沙江", SiteName: "叶巴滩坝上3公里放流点",
		Latitude: ptrF(30.76), Longitude: ptrF(99.02), ReleasedBy: "放流组",
	})
	if err != nil {
		t.Fatalf("放流失败: %v", err)
	}
	if rel.Count != 120 {
		t.Fatalf("放流数量异常: %d", rel.Count)
	}

	trace, err := s.TraceLot(ctx, "LOT-A")
	if err != nil {
		t.Fatal(err)
	}
	if !trace.ConservationOK {
		t.Fatalf("全链路应守恒闭合，未决问题: %v", trace.OpenIssues)
	}
	if len(trace.Stages) != 5 {
		t.Fatalf("应有 5 段交接，实际 %d", len(trace.Stages))
	}
	for _, st := range trace.Stages {
		if st.Result != "BALANCED" {
			t.Fatalf("第 %d 段结果应为 BALANCED，实际 %s", st.Handover.Seq, st.Result)
		}
	}
	if trace.Release == nil || trace.Release.Waterbody != "金沙江" {
		t.Fatalf("追溯视图缺少放流地点: %+v", trace.Release)
	}
	if trace.Effectiveness.Eligible {
		t.Fatal("天然野生鱼批次不应计入增殖放流成效")
	}
}

// TestShortageBlocksUntilConfirmed 核心规则：短少未确认不能流转，确认死亡后自动销账放行。
func TestShortageBlocksUntilConfirmed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-B", domain.OriginWild, 100)
	transfer(t, ctx, s, "LOT-B", "COLLECTION", "SORTING", 100)

	// 155 米轨道提升段少 3 尾。
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-B", FromStage: "SORTING", ToStage: "RAIL_LIFT",
		SentCount: 100, Containers: []ContainerCount{{ContainerRef: "C1", SentCount: 100}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err = s.Receive(ctx, ReceiveInput{
		HandoverID: h.HandoverID,
		Received:   []ContainerCount{{ContainerRef: "C1", ReceivedCount: 97}},
		ReceivedBy: "提升站",
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "DISPUTED" || *h.Discrepancy != 3 {
		t.Fatalf("交接应为 DISPUTED/差额3，实际 %s/%v", h.Status, h.Discrepancy)
	}
	lot, _ := s.GetLot(ctx, "LOT-B")
	if lot.Status != "BLOCKED" || lot.CurrentStage != "SORTING" {
		t.Fatalf("批次应卡在 SORTING/BLOCKED，实际 %s/%s", lot.CurrentStage, lot.Status)
	}

	// 短少未销账，禁止继续向下游发出。
	if _, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-B", FromStage: "SORTING", ToStage: "RAIL_LIFT",
		SentCount: 97, Containers: []ContainerCount{{ContainerRef: "C2", SentCount: 97}},
	}); err == nil || !strings.Contains(err.Error(), "未完成") {
		t.Fatalf("短少未销账时应拒绝再次发出，实际: %v", err)
	}
	// 也不能跳过提升段。
	if _, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-B", FromStage: "SORTING", ToStage: "LAND_TRANSPORT",
		SentCount: 97, Containers: []ContainerCount{{ContainerRef: "C2", SentCount: 97}},
	}); err == nil {
		t.Fatal("不应允许跨环节交接")
	}

	// 先登记设备故障（轨道停车 40 分钟），再登记死亡并关联。
	fault, err := s.RegisterFault(ctx, FaultInput{
		EquipmentRef: "RAIL-HOIST-02", StageCode: "RAIL_LIFT", FaultType: "STOPPAGE",
		Description: "155 米轨道提升机中途停车", LotRefs: []string{"LOT-B"},
		RecordedBy: "机修班",
	})
	if err != nil {
		t.Fatal(err)
	}
	loss, err := s.RegisterLoss(ctx, LossInput{
		LotRef: "LOT-B", HandoverID: h.HandoverID, Count: 3,
		Cause: "提升机停车致缺氧死亡", FaultID: fault.FaultID, RecordedBy: "提升站",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 仅登记未确认：仍然不能流转。
	rec, _ := s.ReconcileHandover(ctx, h.HandoverID)
	if rec.Cleared || rec.PendingLosses != 3 {
		t.Fatalf("待确认死亡不应销账: %+v", rec)
	}
	if _, err := s.ConfirmLoss(ctx, loss.LossID, "环保监督员"); err != nil {
		t.Fatal(err)
	}

	// 确认后交接自动闭合，批次推进到提升段，可以继续。
	h2, err := s.GetHandover(ctx, h.HandoverID)
	if err != nil {
		t.Fatal(err)
	}
	if h2.Status != "CONFIRMED" {
		t.Fatalf("销账后交接应为 CONFIRMED，实际 %s", h2.Status)
	}
	lot, _ = s.GetLot(ctx, "LOT-B")
	if lot.CurrentStage != "RAIL_LIFT" || lot.Status != "IN_CHAIN" {
		t.Fatalf("批次应推进到 RAIL_LIFT，实际 %s/%s", lot.CurrentStage, lot.Status)
	}
	bal, _ := s.Balance(ctx, "LOT-B")
	if bal.Available != 97 || bal.LossesConfirmed != 3 {
		t.Fatalf("账面应为存活97/确认死亡3，实际 %+v", bal)
	}

	// 剩余 97 尾继续到放流点并放流。
	transfer(t, ctx, s, "LOT-B", "RAIL_LIFT", "LAND_TRANSPORT", 97)
	transfer(t, ctx, s, "LOT-B", "LAND_TRANSPORT", "VESSEL_TRANSPORT", 97)
	transfer(t, ctx, s, "LOT-B", "VESSEL_TRANSPORT", "RELEASE_SITE", 97)
	if _, err := s.CreateRelease(ctx, ReleaseInput{
		LotRef: "LOT-B", Count: 97, Waterbody: "金沙江", SiteName: "坝上放流点",
	}); err != nil {
		t.Fatalf("放流失败: %v", err)
	}
	trace, _ := s.TraceLot(ctx, "LOT-B")
	if !trace.ConservationOK {
		t.Fatalf("销账放流后应守恒: %v", trace.OpenIssues)
	}
	if len(trace.Faults) != 1 || trace.Faults[0].EquipmentRef != "RAIL-HOIST-02" {
		t.Fatalf("追溯视图应包含关联设备故障: %+v", trace.Faults)
	}
}

// TestOverReceiveRejected 接收多于发出必须被拒，防止无源增加。
func TestOverReceiveRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-C", domain.OriginWild, 10)
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-C", FromStage: "COLLECTION", ToStage: "SORTING",
		SentCount: 10, Containers: []ContainerCount{{ContainerRef: "C", SentCount: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, ReceiveInput{
		HandoverID: h.HandoverID, ReceivedCount: 11,
	}); err == nil {
		t.Fatal("接收 11 > 发出 10 必须被拒绝")
	}
}

// TestContainerMismatchRejected 箱体合计与交接总数不一致必须被拒。
func TestContainerMismatchRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-D", domain.OriginWild, 10)
	if _, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-D", FromStage: "COLLECTION", ToStage: "SORTING",
		SentCount:  10,
		Containers: []ContainerCount{{ContainerRef: "C1", SentCount: 6}, {ContainerRef: "C2", SentCount: 3}},
	}); err == nil || !strings.Contains(err.Error(), "合计") {
		t.Fatalf("箱体合计 9 与交接 10 不一致应被拒绝，实际: %v", err)
	}
}

// TestRecountGuard 重新清点缩小短少时，已挂接的销账记录不得超过新短少。
func TestRecountGuard(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-RC", domain.OriginWild, 10)
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-RC", FromStage: "COLLECTION", ToStage: "SORTING", SentCount: 10,
		Containers: []ContainerCount{{ContainerRef: "C", SentCount: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Receive(ctx, ReceiveInput{HandoverID: h.HandoverID, ReceivedCount: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterLoss(ctx, LossInput{
		LotRef: "LOT-RC", HandoverID: h.HandoverID, Count: 3, Cause: "缺氧",
	}); err != nil {
		t.Fatal(err)
	}
	// 复核改为短少 1：已挂 3 尾销账，必须先驳回。
	if _, err := s.Receive(ctx, ReceiveInput{HandoverID: h.HandoverID, ReceivedCount: 9}); err == nil {
		t.Fatal("缩小短少至 1 但已挂 3 尾销账，必须拒绝")
	}
}

// TestPerBoxZeroReceived 按箱复核允许某箱清点为 0，并正确计入差额。
func TestPerBoxZeroReceived(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	createLot(t, ctx, s, "LOT-E", domain.OriginWild, 20)
	h, err := s.Dispatch(ctx, DispatchInput{
		LotRef: "LOT-E", FromStage: "COLLECTION", ToStage: "SORTING", SentCount: 20,
		Containers: []ContainerCount{
			{ContainerRef: "C1", SentCount: 12},
			{ContainerRef: "C2", SentCount: 8},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err = s.Receive(ctx, ReceiveInput{
		HandoverID: h.HandoverID,
		Received: []ContainerCount{
			{ContainerRef: "C1", ReceivedCount: 12},
			{ContainerRef: "C2", ReceivedCount: 0},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "DISPUTED" || *h.ReceivedCount != 12 || *h.Discrepancy != 8 {
		t.Fatalf("应短少 8，实际 status=%s received=%v", h.Status, h.ReceivedCount)
	}
}
