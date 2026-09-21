package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// RegisterLoss 登记死亡记录（初始为 PENDING）。
// 关联 handover_id 时用于销记该段交接的短少差额；不关联时表示鱼在站点存活期间死亡。
func (s *Store) RegisterLoss(ctx context.Context, in LossInput) (*LossEvent, error) {
	lot, err := s.GetLot(ctx, in.LotRef)
	if err != nil {
		return nil, err
	}
	if lot.Status == "RELEASED" {
		return nil, conflictf("批次 %s 已放流，放流后的死亡不在过坝链路登记", lot.LotRef)
	}
	if in.Count <= 0 {
		return nil, rulef("死亡数量必须大于 0")
	}
	if in.Cause == "" {
		return nil, rulef("必须填写死亡原因 cause")
	}
	stage := in.StageCode
	if in.HandoverID != "" {
		h, err := getHandoverTx(ctx, s.db, in.HandoverID)
		if err != nil {
			return nil, err
		}
		if h.LotRef != lot.LotRef {
			return nil, rulef("交接单 %s 不属于批次 %s", in.HandoverID, lot.LotRef)
		}
		if h.ReceivedCount == nil {
			return nil, conflictf("交接单 %s 尚未接收清点，不能登记短少死亡", in.HandoverID)
		}
		stage = h.ToStage
		r, err := reconcileHandoverTx(ctx, s.db, in.HandoverID)
		if err != nil {
			return nil, err
		}
		// 已确认与待确认的销账记录都占用短少额度，防止重复申报。
		open := r.Shortage - r.LossesConfirmed - r.ResortsConfirmed - r.PendingLosses - r.PendingResorts
		if open <= 0 {
			return nil, conflictf("交接单 %s 的短少 %d 已全部登记销账记录，不能再挂死亡记录", in.HandoverID, r.Shortage)
		}
		if in.Count > open {
			return nil, rulef("死亡数量 %d 超过该交接单未销账短少 %d", in.Count, open)
		}
	} else {
		if !validStage(stage) {
			return nil, rulef("必须提供合法的 stage_code 或 handover_id")
		}
		if lot.Status != "IN_CHAIN" {
			return nil, conflictf("批次 %s 当前状态为 %s；在途或卡住期间的死亡请挂接到对应交接单", lot.LotRef, lot.Status)
		}
		if lot.CurrentStage != stage {
			return nil, conflictf("批次 %s 当前位于 %s，不能在 %s 登记死亡", lot.LotRef, lot.CurrentStage, stage)
		}
		bal, err := s.Balance(ctx, lot.LotRef)
		if err != nil {
			return nil, err
		}
		// 只统计未挂交接单的 PENDING 死亡；挂交接单的短少已由交接差额锁定。
		var pendingStandalone int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(count),0) FROM loss_events
			 WHERE lot_ref=? AND status='PENDING' AND handover_id IS NULL`,
			lot.LotRef).Scan(&pendingStandalone); err != nil {
			return nil, err
		}
		if in.Count > bal.Available-pendingStandalone {
			return nil, rulef("死亡数量 %d 超过批次当前可登记的存活余额 %d",
				in.Count, bal.Available-pendingStandalone)
		}
	}
	if in.FaultID != "" {
		var ok int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM equipment_faults WHERE fault_id=?`, in.FaultID).Scan(&ok); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, rulef("故障记录 %s 不存在", in.FaultID)
			}
			return nil, err
		}
	}
	occurredAt, err := parseTime(in.OccurredAt)
	if err != nil {
		return nil, err
	}
	e := &LossEvent{
		LossID:     newID("LS"),
		LotRef:     lot.LotRef,
		HandoverID: in.HandoverID,
		StageCode:  stage,
		Count:      in.Count,
		Cause:      in.Cause,
		Status:     "PENDING",
		FaultID:    in.FaultID,
		OccurredAt: occurredAt,
		RecordedBy: in.RecordedBy,
		Notes:      in.Notes,
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO loss_events (loss_id, lot_ref, handover_id, stage_code, count, cause,
		                          status, fault_id, occurred_at, recorded_at, recorded_by, notes)
		 VALUES (?,?,?,?,?,?,'PENDING',?,?,?,?,?)`,
		e.LossID, e.LotRef, nullable(e.HandoverID), e.StageCode, e.Count, e.Cause,
		nullable(e.FaultID), e.OccurredAt, nowText(), nullable(e.RecordedBy), nullable(e.Notes)); err != nil {
		return nil, err
	}
	return e, nil
}

// ConfirmLoss 确认死亡记录。只有 CONFIRMED 的死亡才能销记交接短少、扣减批次账面。
func (s *Store) ConfirmLoss(ctx context.Context, lossID, confirmedBy string) (*LossEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	e, err := getLossTx(ctx, tx, lossID)
	if err != nil {
		return nil, err
	}
	if e.Status != "PENDING" {
		return nil, conflictf("死亡记录 %s 当前状态为 %s，只能确认 PENDING 记录", lossID, e.Status)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE loss_events SET status='CONFIRMED', confirmed_at=?, confirmed_by=? WHERE loss_id=?`,
		nowText(), nullable(confirmedBy), lossID); err != nil {
		return nil, err
	}
	e.Status = "CONFIRMED"
	if e.HandoverID != "" {
		if err := tryClearHandover(ctx, tx, e.HandoverID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLoss(ctx, lossID)
}

// RejectLoss 驳回死亡记录（如查验认定非死亡原因）。
func (s *Store) RejectLoss(ctx context.Context, lossID, confirmedBy, note string) (*LossEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	e, err := getLossTx(ctx, tx, lossID)
	if err != nil {
		return nil, err
	}
	if e.Status != "PENDING" {
		return nil, conflictf("死亡记录 %s 当前状态为 %s，不能驳回", lossID, e.Status)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE loss_events SET status='REJECTED', confirmed_at=?, confirmed_by=?,
		      notes=COALESCE(NULLIF(?, ''), notes) WHERE loss_id=?`,
		nowText(), nullable(confirmedBy), note, lossID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLoss(ctx, lossID)
}

// GetLoss 查询单条死亡记录。
func (s *Store) GetLoss(ctx context.Context, lossID string) (*LossEvent, error) {
	return getLossTx(ctx, s.db, lossID)
}

func getLossTx(ctx context.Context, q queryable, lossID string) (*LossEvent, error) {
	e := &LossEvent{}
	var handoverID, faultID, confirmedAt, recordedBy, confirmedBy, notes sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT loss_id, lot_ref, handover_id, stage_code, count, cause, status, fault_id,
		        occurred_at, confirmed_at, recorded_by, confirmed_by, COALESCE(notes,'')
		 FROM loss_events WHERE loss_id=?`, lossID).
		Scan(&e.LossID, &e.LotRef, &handoverID, &e.StageCode, &e.Count, &e.Cause, &e.Status,
			&faultID, &e.OccurredAt, &confirmedAt, &recordedBy, &confirmedBy, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.HandoverID = handoverID.String
	e.FaultID = faultID.String
	e.ConfirmedAt = confirmedAt.String
	e.RecordedBy = recordedBy.String
	e.ConfirmedBy = confirmedBy.String
	e.Notes = notes.String
	return e, nil
}

// ListLosses 返回批次的死亡记录，按发生时间排序。
func (s *Store) ListLosses(ctx context.Context, lotRef string) ([]LossEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT loss_id FROM loss_events WHERE lot_ref=? ORDER BY occurred_at, loss_id`, lotRef)
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
	out := make([]LossEvent, 0, len(ids))
	for _, id := range ids {
		e, err := s.GetLoss(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, nil
}

func validStage(stage string) bool {
	return stage != "" && domain.StageIndex(domain.StageCode(stage)) >= 0
}
