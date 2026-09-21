package store

import (
	"context"
	"strconv"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// StageCheck 是一次交接的守恒核验结果。
type StageCheck struct {
	Handover       Handover               `json:"handover"`
	Reconciliation HandoverReconciliation `json:"reconciliation"`
	Losses         []LossEvent            `json:"losses"`  // 挂在该交接上的死亡记录
	Resorts        []ResortEvent          `json:"resorts"` // 挂在该交接上的重分拣记录
	// Result: IN_TRANSIT 已发出未接收；BALANCED 守恒闭合；BLOCKED 短少未销账
	Result string `json:"result"`
}

// EffectivenessSummary 是增殖放流成效口径：只有 HATCHERY 批次可计入，
// 且只统计标记匹配（MATCHED）的回捕，无标记野生鱼不计入。
type EffectivenessSummary struct {
	Eligible               bool   `json:"eligible"`         // 是否可计入增殖放流成效
	Reason                 string `json:"reason,omitempty"` // 不可计入的原因
	ReleasedCount          int    `json:"released_count"`
	MarkedCount            int    `json:"marked_count"`            // 标记覆盖鱼尾数
	MatchedRecaptureCount  int    `json:"matched_recapture_count"` // 标记命中的回捕鱼尾数
	MatchedRecaptureEvents int    `json:"matched_recapture_events"`
	SurveyCount            int    `json:"survey_count"` // 命中记录涉及的调查次数
}

// LotTrace 是管理部门查询一个批次时看到的全链路核验视图。
type LotTrace struct {
	Lot
	Balance          LotBalance           `json:"balance"`
	ConservationOK   bool                 `json:"conservation_ok"` // 全链路数量是否闭合
	OpenIssues       []string             `json:"open_issues"`
	Stages           []StageCheck         `json:"stages"`
	StandaloneLosses []LossEvent          `json:"standalone_losses"`
	Resorts          []ResortEvent        `json:"resorts"`
	Faults           []EquipmentFault     `json:"faults"`
	Release          *Release             `json:"release,omitempty"`
	Marks            []Mark               `json:"marks"`
	Recaptures       []RecaptureDetail    `json:"recaptures"`
	Effectiveness    EffectivenessSummary `json:"effectiveness"`
}

// TraceLot 汇总一个批次从登记到放流后监测的完整核验链路。
func (s *Store) TraceLot(ctx context.Context, lotRef string) (*LotTrace, error) {
	lot, err := s.GetLot(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	bal, err := s.Balance(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	trace := &LotTrace{
		Lot:              *lot,
		Balance:          bal,
		OpenIssues:       []string{},
		Stages:           []StageCheck{},
		StandaloneLosses: []LossEvent{},
	}

	losses, err := s.ListLosses(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	resorts, err := s.ListResorts(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	faults, err := s.ListFaults(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	marks, err := s.ListMarks(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	recaps, err := s.ListRecaptures(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	trace.Resorts = resorts
	trace.Faults = faults
	trace.Marks = marks
	trace.Recaptures = recaps
	for i := range losses {
		if losses[i].HandoverID == "" {
			trace.StandaloneLosses = append(trace.StandaloneLosses, losses[i])
		}
	}

	handovers, err := s.ListHandovers(ctx, lotRef)
	if err != nil {
		return nil, err
	}
	lossByHO := map[string][]LossEvent{}
	for _, l := range losses {
		if l.HandoverID != "" {
			lossByHO[l.HandoverID] = append(lossByHO[l.HandoverID], l)
		}
	}
	resortByHO := map[string][]ResortEvent{}
	for _, r := range resorts {
		if r.HandoverID != "" {
			resortByHO[r.HandoverID] = append(resortByHO[r.HandoverID], r)
		}
	}
	for _, h := range handovers {
		rec, err := s.ReconcileHandover(ctx, h.HandoverID)
		if err != nil {
			return nil, err
		}
		check := StageCheck{
			Handover:       h,
			Reconciliation: rec,
			Losses:         lossByHO[h.HandoverID],
			Resorts:        resortByHO[h.HandoverID],
		}
		switch {
		case h.ReceivedCount == nil:
			check.Result = "IN_TRANSIT"
			trace.OpenIssues = append(trace.OpenIssues,
				"第 "+strconv.Itoa(h.Seq)+" 段交接（"+h.FromStage+"→"+h.ToStage+"）已发出但尚未接收清点")
		case rec.Shortage == 0:
			check.Result = "BALANCED"
		case rec.Cleared:
			check.Result = "BALANCED"
		default:
			check.Result = "BLOCKED"
			trace.OpenIssues = append(trace.OpenIssues,
				"第 "+strconv.Itoa(h.Seq)+" 段交接（"+h.FromStage+"→"+h.ToStage+
					"）短少 "+strconv.Itoa(rec.Shortage)+" 尾，已确认销账 "+
					strconv.Itoa(rec.LossesConfirmed+rec.ResortsConfirmed)+" 尾，仍有 "+
					strconv.Itoa(rec.Shortage-rec.LossesConfirmed-rec.ResortsConfirmed)+" 尾缺少经确认的死亡或重分拣记录")
		}
		trace.Stages = append(trace.Stages, check)
	}

	// 账面恒等式：存活 + 确认死亡 + 已放流 = 初登 + 重分拣入 - 重分拣出
	identityLHS := bal.Available + bal.LossesConfirmed + bal.Released
	identityRHS := bal.Initial + bal.ResortedIn - bal.ResortedOut
	if identityLHS != identityRHS {
		trace.OpenIssues = append(trace.OpenIssues,
			"账面恒等式不平：存活 "+strconv.Itoa(bal.Available)+" + 死亡 "+strconv.Itoa(bal.LossesConfirmed)+
				" + 放流 "+strconv.Itoa(bal.Released)+" ≠ 初登 "+strconv.Itoa(bal.Initial)+" + 划入 "+
				strconv.Itoa(bal.ResortedIn)+" - 划出 "+strconv.Itoa(bal.ResortedOut))
	}
	trace.ConservationOK = len(trace.OpenIssues) == 0

	if release, err := s.GetRelease(ctx, lotRef); err == nil {
		trace.Release = release
	} else if err != ErrNotFound {
		return nil, err
	}

	// 放流成效口径。
	eff := EffectivenessSummary{}
	surveySet := map[string]bool{}
	for _, m := range marks {
		eff.MarkedCount += m.MarkedCount
	}
	for _, r := range recaps {
		if r.MatchStatus == "MATCHED" {
			eff.MatchedRecaptureCount += r.Count
			eff.MatchedRecaptureEvents++
			surveySet[r.SurveyID] = true
		}
	}
	eff.SurveyCount = len(surveySet)
	if lot.Origin == domain.OriginHatchery {
		eff.Eligible = true
	} else {
		eff.Reason = "批次来源为天然野生鱼（WILD），过坝放流不计入增殖放流成效"
	}
	if trace.Release != nil {
		eff.ReleasedCount = trace.Release.Count
	}
	trace.Effectiveness = eff

	return trace, nil
}
