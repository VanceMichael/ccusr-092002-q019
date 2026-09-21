package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// queryable 是 *sql.DB 与 *sql.Tx 的共同读接口。
type queryable interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execable interface {
	queryable
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Dispatch 登记一段交接的发出（集鱼槽→分拣→轨道提升→陆运→船运→放流点）。
// 整批随箱交接：sent_count 必须等于批次账面存活数，避免同批鱼被重复发出。
func (s *Store) Dispatch(ctx context.Context, in DispatchInput) (*Handover, error) {
	lot, err := s.GetLot(ctx, in.LotRef)
	if err != nil {
		return nil, err
	}
	if !domain.ValidStage(in.FromStage) || !domain.ValidStage(in.ToStage) {
		return nil, rulef("from_stage/to_stage 必须是合法环节代码")
	}
	if !domain.IsAdjacentTransfer(domain.StageCode(in.FromStage), domain.StageCode(in.ToStage)) {
		return nil, rulef("交接必须沿标准链路相邻环节进行: %s → %s", in.FromStage, in.ToStage)
	}
	if lot.Status == "RELEASED" {
		return nil, conflictf("批次 %s 已放流，不能再交接", lot.LotRef)
	}
	if lot.Status != "IN_CHAIN" {
		return nil, conflictf("批次 %s 有未完成的交接（当前状态 %s），不能再次发出", lot.LotRef, lot.Status)
	}
	if lot.CurrentStage != in.FromStage {
		return nil, conflictf("批次 %s 当前位于 %s，不能从 %s 发出", lot.LotRef, lot.CurrentStage, in.FromStage)
	}
	if in.SentCount <= 0 {
		return nil, rulef("sent_count 必须大于 0")
	}
	if err := validateContainers(in.Containers, in.SentCount); err != nil {
		return nil, err
	}
	bal, err := s.Balance(ctx, lot.LotRef)
	if err != nil {
		return nil, err
	}
	if err := s.guardPendingAdj(ctx, lot.LotRef); err != nil {
		return nil, err
	}
	if in.SentCount != bal.Available {
		return nil, rulef("整批交接要求 sent_count=%d 等于账面存活数 %d（初登 %d + 重分拣入 %d - 重分拣出 %d - 确认死亡 %d - 已放流 %d）",
			in.SentCount, bal.Available, bal.Initial, bal.ResortedIn, bal.ResortedOut, bal.LossesConfirmed, bal.Released)
	}
	sentAt, err := parseTime(in.SentAt)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var seq int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(seq),0)+1 FROM handovers WHERE lot_ref=?`, lot.LotRef).Scan(&seq); err != nil {
		return nil, err
	}
	h := &Handover{
		HandoverID: newID("HO"),
		LotRef:     lot.LotRef,
		Seq:        seq,
		FromStage:  in.FromStage,
		ToStage:    in.ToStage,
		SentCount:  in.SentCount,
		Status:     "SENT",
		SentAt:     sentAt,
		SentBy:     in.SentBy,
		Note:       in.Note,
		Containers: in.Containers,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO handovers (handover_id, lot_ref, seq, from_stage, to_stage,
		                        sent_count, status, sent_at, sent_by, note, recorded_at)
		 VALUES (?,?,?,?,?,?, 'SENT', ?,?,?,?)`,
		h.HandoverID, h.LotRef, h.Seq, h.FromStage, h.ToStage, h.SentCount,
		h.SentAt, nullable(h.SentBy), nullable(h.Note), nowText()); err != nil {
		return nil, err
	}
	if err := upsertContainers(ctx, tx, h.HandoverID, h.Containers); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE lots SET status='IN_TRANSIT' WHERE lot_ref=?`, lot.LotRef); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return h, nil
}

// GetHandover 查询交接单（含箱体明细）。
func (s *Store) GetHandover(ctx context.Context, handoverID string) (*Handover, error) {
	return getHandoverTx(ctx, s.db, handoverID)
}

func getHandoverTx(ctx context.Context, q queryable, handoverID string) (*Handover, error) {
	h := &Handover{}
	var received sql.NullInt64
	var discrepancy sql.NullInt64
	var receivedAt, sentBy, receivedBy, note sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT handover_id, lot_ref, seq, from_stage, to_stage, sent_count,
		        received_count, status, discrepancy, sent_at, received_at, sent_by, received_by, note
		 FROM handovers WHERE handover_id=?`, handoverID).
		Scan(&h.HandoverID, &h.LotRef, &h.Seq, &h.FromStage, &h.ToStage, &h.SentCount,
			&received, &h.Status, &discrepancy, &h.SentAt, &receivedAt, &sentBy, &receivedBy, &note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if received.Valid {
		v := int(received.Int64)
		h.ReceivedCount = &v
	}
	if discrepancy.Valid {
		v := int(discrepancy.Int64)
		h.Discrepancy = &v
	}
	h.ReceivedAt = receivedAt.String
	h.SentBy = sentBy.String
	h.ReceivedBy = receivedBy.String
	h.Note = note.String
	rows, err := q.QueryContext(ctx,
		`SELECT container_ref, sent_count, received_count
		 FROM handover_containers WHERE handover_id=? ORDER BY container_ref`, handoverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ContainerCount
		var rcv sql.NullInt64
		if err := rows.Scan(&c.ContainerRef, &c.SentCount, &rcv); err != nil {
			return nil, err
		}
		if rcv.Valid {
			c.ReceivedCount = int(rcv.Int64)
		}
		h.Containers = append(h.Containers, c)
	}
	return h, rows.Err()
}

// Receive 登记交接接收清点。数量一致即确认；短少则挂 DISPUTED，
// 必须用经确认的死亡记录或重新分拣记录把差额销账后才能继续流转。
func (s *Store) Receive(ctx context.Context, in ReceiveInput) (*Handover, error) {
	if in.HandoverID == "" {
		return nil, rulef("handover_id 不能为空")
	}
	receivedAt, err := parseTime(in.ReceivedAt)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	h, err := getHandoverTx(ctx, tx, in.HandoverID)
	if err != nil {
		return nil, err
	}
	if h.Status != "SENT" && h.Status != "DISPUTED" {
		return nil, conflictf("交接单 %s 已 %s，不能重复接收确认", h.HandoverID, h.Status)
	}

	containers, totalReceived, perBox, err := mergeReceived(h, in)
	if err != nil {
		return nil, err
	}
	if totalReceived > h.SentCount {
		return nil, rulef("接收数量 %d 多于发出数量 %d，不允许无源增加；物种复核请在分拣环节登记重新分拣",
			totalReceived, h.SentCount)
	}
	// 重新清点使短少变小时，已挂接的销账记录（含待确认）不得超过新短少，
	// 否则会出现“销账多于实缺”的账面漏洞；应先驳回多余的死亡/重分拣记录。
	if h.ReceivedCount != nil && totalReceived > *h.ReceivedCount {
		var attached int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(c),0) FROM (
				SELECT count AS c FROM loss_events WHERE handover_id=? AND status IN ('PENDING','CONFIRMED')
				UNION ALL
				SELECT count FROM resort_events WHERE handover_id=? AND status IN ('PENDING','CONFIRMED')
			)`, h.HandoverID, h.HandoverID).Scan(&attached); err != nil {
			return nil, err
		}
		if newShort := h.SentCount - totalReceived; attached > newShort {
			return nil, conflictf("新短少 %d 小于已挂接销账记录合计 %d，请先驳回多余的死亡或重分拣记录",
				newShort, attached)
		}
	}

	short := h.SentCount - totalReceived
	newStatus := "CONFIRMED"
	lotStatus := "IN_CHAIN"
	if short > 0 {
		newStatus = "DISPUTED"
		lotStatus = "BLOCKED"
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE handovers
		 SET received_count=?, status=?, discrepancy=?, received_at=?, received_by=?,
		     note=COALESCE(NULLIF(?, ''), note)
		 WHERE handover_id=?`,
		totalReceived, newStatus, nullIfZero(short), receivedAt, nullable(in.ReceivedBy),
		in.Note, h.HandoverID); err != nil {
		return nil, err
	}
	if err := replaceContainers(ctx, tx, h.HandoverID, containers, perBox); err != nil {
		return nil, err
	}
	if newStatus == "CONFIRMED" {
		if err := advanceOnConfirmed(ctx, tx, h, lotStatus); err != nil {
			return nil, err
		}
	} else if _, err := tx.ExecContext(ctx,
		`UPDATE lots SET status='BLOCKED' WHERE lot_ref=?`, h.LotRef); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return getHandoverTx(ctx, s.db, h.HandoverID)
}

// mergeReceived 合并接收清点：可按箱复核，也可只给整单 received_count。
// 返回 perBox=true 时箱体级接收数量（含 0）应落库。
func mergeReceived(h *Handover, in ReceiveInput) (out []ContainerCount, total int, perBox bool, err error) {
	if len(in.Received) == 0 {
		if in.ReceivedCount <= 0 {
			return nil, 0, false, rulef("必须提供按箱接收数量或大于 0 的 received_count")
		}
		return h.Containers, in.ReceivedCount, false, nil
	}
	byRef := map[string]ContainerCount{}
	for _, c := range h.Containers {
		byRef[c.ContainerRef] = c
	}
	out = make([]ContainerCount, 0, len(in.Received))
	seen := map[string]bool{}
	for _, r := range in.Received {
		if seen[r.ContainerRef] {
			return nil, 0, false, rulef("箱体 %s 重复清点", r.ContainerRef)
		}
		seen[r.ContainerRef] = true
		sent, ok := byRef[r.ContainerRef]
		if !ok {
			return nil, 0, false, rulef("箱体 %s 不在交接单 %s 的发出清单中", r.ContainerRef, h.HandoverID)
		}
		if r.ReceivedCount < 0 {
			return nil, 0, false, rulef("箱体 %s 接收数量不能为负", r.ContainerRef)
		}
		if r.ReceivedCount > sent.SentCount {
			return nil, 0, false, rulef("箱体 %s 接收 %d 多于发出 %d", r.ContainerRef, r.ReceivedCount, sent.SentCount)
		}
		out = append(out, ContainerCount{
			ContainerRef:  sent.ContainerRef,
			SentCount:     sent.SentCount,
			ReceivedCount: r.ReceivedCount,
		})
		total += r.ReceivedCount
	}
	if len(out) != len(h.Containers) {
		return nil, 0, false, rulef("按箱复核必须覆盖交接单上的全部 %d 个箱体，实际收到 %d 个",
			len(h.Containers), len(out))
	}
	if in.ReceivedCount > 0 && in.ReceivedCount != total {
		return nil, 0, false, rulef("received_count=%d 与按箱合计 %d 不一致", in.ReceivedCount, total)
	}
	return out, total, true, nil
}

// validateContainers 校验箱体清单与合计。
func validateContainers(cs []ContainerCount, total int) error {
	if len(cs) == 0 {
		return rulef("每个交接至少登记一个运输箱（container_ref）")
	}
	sum := 0
	seen := map[string]bool{}
	for _, c := range cs {
		if c.ContainerRef == "" {
			return rulef("container_ref 不能为空")
		}
		if seen[c.ContainerRef] {
			return rulef("箱体 %s 在同一交接单中重复登记", c.ContainerRef)
		}
		seen[c.ContainerRef] = true
		if c.SentCount < 0 {
			return rulef("箱体 %s 数量不能为负", c.ContainerRef)
		}
		sum += c.SentCount
	}
	if sum != total {
		return rulef("箱体数量合计 %d 与交接数量 %d 不一致", sum, total)
	}
	return nil
}

// HandoverReconciliation 是一段交接的销账结果。
type HandoverReconciliation struct {
	Shortage         int  `json:"shortage"`
	LossesConfirmed  int  `json:"losses_confirmed"`
	ResortsConfirmed int  `json:"resorts_confirmed"`
	PendingLosses    int  `json:"pending_losses"`
	PendingResorts   int  `json:"pending_resorts"`
	Cleared          bool `json:"cleared"`
}

// ReconcileHandover 返回交接短少与已确认/待确认的销账记录。
func (s *Store) ReconcileHandover(ctx context.Context, handoverID string) (HandoverReconciliation, error) {
	return reconcileHandoverTx(ctx, s.db, handoverID)
}

func reconcileHandoverTx(ctx context.Context, q queryable, handoverID string) (HandoverReconciliation, error) {
	var r HandoverReconciliation
	var status string
	var sent int
	var received sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT status, sent_count, received_count FROM handovers WHERE handover_id=?`,
		handoverID).Scan(&status, &sent, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if !received.Valid {
		return r, nil // 尚未接收清点
	}
	r.Shortage = sent - int(received.Int64)
	for _, row := range []struct {
		kind   string
		target *int
	}{
		{"CONFIRMED", &r.LossesConfirmed},
	} {
		if err := q.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(count),0) FROM loss_events WHERE handover_id=? AND status=?`,
			handoverID, row.kind).Scan(row.target); err != nil {
			return r, err
		}
	}
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM resort_events WHERE handover_id=? AND status='CONFIRMED'`,
		handoverID).Scan(&r.ResortsConfirmed); err != nil {
		return r, err
	}
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM loss_events WHERE handover_id=? AND status='PENDING'`,
		handoverID).Scan(&r.PendingLosses); err != nil {
		return r, err
	}
	if err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM resort_events WHERE handover_id=? AND status='PENDING'`,
		handoverID).Scan(&r.PendingResorts); err != nil {
		return r, err
	}
	if r.Shortage == 0 || r.LossesConfirmed+r.ResortsConfirmed >= r.Shortage {
		r.Cleared = true
	}
	return r, nil
}

// ListHandovers 返回批次的逐段交接。
func (s *Store) ListHandovers(ctx context.Context, lotRef string) ([]Handover, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT handover_id FROM handovers WHERE lot_ref=? ORDER BY seq`, lotRef)
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
	out := make([]Handover, 0, len(ids))
	for _, id := range ids {
		h, err := getHandoverTx(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, nil
}

// tryClearHandover 在死亡/重分拣被确认后尝试销账一段短少交接；
// 销账成功时把 DISPUTED 交接转为 CONFIRMED 并推进批次环节。
func tryClearHandover(ctx context.Context, tx *sql.Tx, handoverID string) error {
	r, err := reconcileHandoverTx(ctx, tx, handoverID)
	if err != nil {
		return err
	}
	if r.Shortage == 0 || !r.Cleared {
		return nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE handovers SET status='CONFIRMED' WHERE handover_id=? AND status='DISPUTED'`,
		handoverID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil
	}
	h, err := getHandoverTx(ctx, tx, handoverID)
	if err != nil {
		return err
	}
	return advanceOnConfirmed(ctx, tx, h, "IN_CHAIN")
}

// advanceOnConfirmed 把批次推进到交接的目标环节并恢复可流转状态。
func advanceOnConfirmed(ctx context.Context, q execable, h *Handover, lotStatus string) error {
	var currentStage string
	if err := q.QueryRowContext(ctx,
		`SELECT current_stage FROM lots WHERE lot_ref=?`, h.LotRef).Scan(&currentStage); err != nil {
		return err
	}
	if domain.StageIndex(domain.StageCode(currentStage)) < domain.StageIndex(domain.StageCode(h.ToStage)) {
		if _, err := q.ExecContext(ctx,
			`UPDATE lots SET current_stage=?, status=? WHERE lot_ref=?`,
			h.ToStage, lotStatus, h.LotRef); err != nil {
			return err
		}
	} else if _, err := q.ExecContext(ctx,
		`UPDATE lots SET status=? WHERE lot_ref=? AND status!='RELEASED'`, lotStatus, h.LotRef); err != nil {
		return err
	}
	return nil
}

func upsertContainers(ctx context.Context, q execable, handoverID string, cs []ContainerCount) error {
	for _, c := range cs {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO containers(container_ref, first_seen_at) VALUES (?,?)
			 ON CONFLICT(container_ref) DO NOTHING`, c.ContainerRef, nowText()); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO handover_containers (handover_id, container_ref, sent_count, received_count)
			 VALUES (?,?,?,NULL)`, handoverID, c.ContainerRef, c.SentCount); err != nil {
			return err
		}
	}
	return nil
}

// replaceContainers 重写箱体明细；perBox=false（整单清点）时保留接收数为空。
func replaceContainers(ctx context.Context, q execable, handoverID string, cs []ContainerCount, perBox bool) error {
	if _, err := q.ExecContext(ctx,
		`DELETE FROM handover_containers WHERE handover_id=?`, handoverID); err != nil {
		return err
	}
	for _, c := range cs {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO containers(container_ref, first_seen_at) VALUES (?,?)
			 ON CONFLICT(container_ref) DO NOTHING`, c.ContainerRef, nowText()); err != nil {
			return err
		}
		rc := sql.NullInt64{}
		if perBox {
			rc = sql.NullInt64{Int64: int64(c.ReceivedCount), Valid: true}
		}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO handover_containers (handover_id, container_ref, sent_count, received_count)
			 VALUES (?,?,?,?)`, handoverID, c.ContainerRef, c.SentCount, rc); err != nil {
			return err
		}
	}
	return nil
}

func nullable(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullIfZero(n int) sql.NullInt64 {
	if n == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(n), Valid: true}
}
