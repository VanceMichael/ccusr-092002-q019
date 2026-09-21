package store

import (
	"context"
	"database/sql"
	"errors"
)

// RegisterResort 登记重新分拣：把 count 尾鱼从 from 批次划到同阶段的 to 批次。
// 挂 handover_id 时用于销记交接短少（如复核发现短少的鱼属于另一物种）；
// to 批次留空时按 to_species_code 自动新建零初始数量批次。
func (s *Store) RegisterResort(ctx context.Context, in ResortInput) (*ResortEvent, error) {
	from, err := s.GetLot(ctx, in.FromLotRef)
	if err != nil {
		return nil, err
	}
	if in.Count <= 0 {
		return nil, rulef("重新分拣数量必须大于 0")
	}
	if in.Reason == "" {
		return nil, rulef("必须填写重新分拣原因 reason（如物种复核）")
	}

	stage := in.StageCode
	if in.HandoverID != "" {
		h, err := getHandoverTx(ctx, s.db, in.HandoverID)
		if err != nil {
			return nil, err
		}
		if h.LotRef != from.LotRef {
			return nil, rulef("交接单 %s 不属于批次 %s", in.HandoverID, from.LotRef)
		}
		if h.ReceivedCount == nil {
			return nil, conflictf("交接单 %s 尚未接收清点，不能登记短少重分拣", in.HandoverID)
		}
		stage = h.ToStage
		r, err := reconcileHandoverTx(ctx, s.db, in.HandoverID)
		if err != nil {
			return nil, err
		}
		open := r.Shortage - r.LossesConfirmed - r.ResortsConfirmed - r.PendingLosses - r.PendingResorts
		if open <= 0 {
			return nil, conflictf("交接单 %s 的短少 %d 已全部登记销账记录", in.HandoverID, r.Shortage)
		}
		if in.Count > open {
			return nil, rulef("重分拣数量 %d 超过该交接单未销账短少 %d", in.Count, open)
		}
	} else {
		if !validStage(stage) {
			return nil, rulef("必须提供合法的 stage_code 或 handover_id")
		}
		if from.Status != "IN_CHAIN" {
			return nil, conflictf("批次 %s 当前状态 %s，站点重分拣只能在可流转状态进行", from.LotRef, from.Status)
		}
		if from.CurrentStage != stage {
			return nil, conflictf("批次 %s 当前位于 %s，不能在 %s 重新分拣", from.LotRef, from.CurrentStage, stage)
		}
		bal, err := s.Balance(ctx, from.LotRef)
		if err != nil {
			return nil, err
		}
		var pendingOut int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(count),0) FROM resort_events
			 WHERE from_lot_ref=? AND status='PENDING' AND handover_id IS NULL`,
			from.LotRef).Scan(&pendingOut); err != nil {
			return nil, err
		}
		if in.Count > bal.Available-pendingOut {
			return nil, rulef("重分拣数量 %d 超过批次当前可动用存活余额 %d", in.Count, bal.Available-pendingOut)
		}
	}

	to, err := s.resolveResortTarget(ctx, in, from, stage)
	if err != nil {
		return nil, err
	}
	occurredAt, err := parseTime(in.OccurredAt)
	if err != nil {
		return nil, err
	}

	ev := &ResortEvent{
		ResortID:   newID("RS"),
		FromLotRef: from.LotRef,
		ToLotRef:   to.LotRef,
		HandoverID: in.HandoverID,
		StageCode:  stage,
		Count:      in.Count,
		Reason:     in.Reason,
		Status:     "PENDING",
		OccurredAt: occurredAt,
		RecordedBy: in.RecordedBy,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if to.createdHere {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO lots (lot_ref, species_code, origin, initial_count, entry_stage,
			                   current_stage, status, created_by_resort, notes, created_at)
			 VALUES (?,?,?,0,?,?, 'IN_CHAIN', 1, ?, ?)`,
			to.LotRef, to.SpeciesCode, to.Origin, stage, stage,
			"由批次 "+from.LotRef+" 重新分拣产生", nowText()); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO resort_events (resort_id, from_lot_ref, to_lot_ref, handover_id, stage_code,
		                            count, reason, status, occurred_at, recorded_at, recorded_by)
		 VALUES (?,?,?,?,?,?,?,'PENDING',?,?,?)`,
		ev.ResortID, ev.FromLotRef, ev.ToLotRef, nullable(ev.HandoverID), ev.StageCode,
		ev.Count, ev.Reason, ev.OccurredAt, nowText(), nullable(ev.RecordedBy)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// resortTarget 携带重分拣目标批次信息。
type resortTarget struct {
	*Lot
	createdHere bool
}

// resolveResortTarget 找到或校验重分拣目标批次；来源（野生/增殖）不允许借重分拣改变。
func (s *Store) resolveResortTarget(ctx context.Context, in ResortInput, from *Lot, stage string) (*resortTarget, error) {
	if in.ToLotRef != "" {
		to, err := s.GetLot(ctx, in.ToLotRef)
		if err != nil {
			return nil, err
		}
		if to.LotRef == from.LotRef {
			return nil, rulef("重新分拣的目标批次不能与来源批次相同")
		}
		if to.Origin != from.Origin {
			return nil, rulef("重分拣不允许改变鱼的来源：来源批次为 %s，目标批次为 %s", from.Origin, to.Origin)
		}
		if to.Status == "RELEASED" {
			return nil, conflictf("目标批次 %s 已放流", to.LotRef)
		}
		if to.CurrentStage != stage {
			return nil, conflictf("目标批次 %s 当前位于 %s，重分拣发生在 %s，两者必须同段",
				to.LotRef, to.CurrentStage, stage)
		}
		return &resortTarget{Lot: to}, nil
	}
	if in.ToSpecies == "" {
		return nil, rulef("目标批次为空时必须提供 to_species_code")
	}
	origin := in.ToOrigin
	if origin == "" {
		origin = from.Origin
	}
	if origin != from.Origin {
		return nil, rulef("重分拣不允许改变鱼的来源：新批次来源必须为 %s", from.Origin)
	}
	return &resortTarget{
		createdHere: true,
		Lot: &Lot{
			LotRef:       newID("LOT"),
			SpeciesCode:  in.ToSpecies,
			Origin:       origin,
			EntryStage:   stage,
			CurrentStage: stage,
		},
	}, nil
}

// ConfirmResort 确认重新分拣，确认后鱼才真正划入目标批次并销记交接短少。
func (s *Store) ConfirmResort(ctx context.Context, resortID, confirmedBy string) (*ResortEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ev, err := getResortTx(ctx, tx, resortID)
	if err != nil {
		return nil, err
	}
	if ev.Status != "PENDING" {
		return nil, conflictf("重分拣记录 %s 当前状态为 %s，只能确认 PENDING 记录", resortID, ev.Status)
	}
	var toStatus string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM lots WHERE lot_ref=?`, ev.ToLotRef).Scan(&toStatus); err != nil {
		return nil, err
	}
	if toStatus == "RELEASED" {
		return nil, conflictf("目标批次 %s 已放流，不能再划入鱼", ev.ToLotRef)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE resort_events SET status='CONFIRMED', confirmed_at=?, confirmed_by=? WHERE resort_id=?`,
		nowText(), nullable(confirmedBy), resortID); err != nil {
		return nil, err
	}
	ev.Status = "CONFIRMED"
	if ev.HandoverID != "" {
		if err := tryClearHandover(ctx, tx, ev.HandoverID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetResort(ctx, resortID)
}

// RejectResort 驳回重新分拣记录。
func (s *Store) RejectResort(ctx context.Context, resortID, confirmedBy string) (*ResortEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ev, err := getResortTx(ctx, tx, resortID)
	if err != nil {
		return nil, err
	}
	if ev.Status != "PENDING" {
		return nil, conflictf("重分拣记录 %s 当前状态为 %s，不能驳回", resortID, ev.Status)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE resort_events SET status='REJECTED', confirmed_at=?, confirmed_by=? WHERE resort_id=?`,
		nowText(), nullable(confirmedBy), resortID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetResort(ctx, resortID)
}

// GetResort 查询单条重分拣记录。
func (s *Store) GetResort(ctx context.Context, resortID string) (*ResortEvent, error) {
	return getResortTx(ctx, s.db, resortID)
}

func getResortTx(ctx context.Context, q queryable, resortID string) (*ResortEvent, error) {
	ev := &ResortEvent{}
	var handoverID, reason, confirmedAt, recordedBy, confirmedBy sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT resort_id, from_lot_ref, to_lot_ref, handover_id, stage_code, count,
		        reason, status, occurred_at, confirmed_at, recorded_by, confirmed_by
		 FROM resort_events WHERE resort_id=?`, resortID).
		Scan(&ev.ResortID, &ev.FromLotRef, &ev.ToLotRef, &handoverID, &ev.StageCode, &ev.Count,
			&reason, &ev.Status, &ev.OccurredAt, &confirmedAt, &recordedBy, &confirmedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ev.HandoverID = handoverID.String
	ev.Reason = reason.String
	ev.ConfirmedAt = confirmedAt.String
	ev.RecordedBy = recordedBy.String
	ev.ConfirmedBy = confirmedBy.String
	return ev, nil
}

// ListResorts 返回与批次相关（划出或划入）的重分拣记录。
func (s *Store) ListResorts(ctx context.Context, lotRef string) ([]ResortEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT resort_id FROM resort_events
		 WHERE from_lot_ref=? OR to_lot_ref=?
		 ORDER BY occurred_at, resort_id`, lotRef, lotRef)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]ResortEvent, 0, len(ids))
	for _, id := range ids {
		ev, err := s.GetResort(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *ev)
	}
	return out, nil
}
