package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// RegisterFault 登记设备故障。即使没有死亡也要留痕；可关联当时受影响的批次。
func (s *Store) RegisterFault(ctx context.Context, in FaultInput) (*EquipmentFault, error) {
	if in.EquipmentRef == "" {
		return nil, rulef("equipment_ref 不能为空")
	}
	if !validStage(in.StageCode) {
		return nil, rulef("stage_code 不合法")
	}
	if in.FaultType == "" {
		return nil, rulef("fault_type 不能为空")
	}
	startedAt, err := parseTime(in.StartedAt)
	if err != nil {
		return nil, err
	}
	for _, ref := range in.LotRefs {
		if _, err := s.GetLot(ctx, ref); err != nil {
			return nil, err
		}
	}
	f := &EquipmentFault{
		FaultID:      in.FaultID,
		EquipmentRef: in.EquipmentRef,
		StageCode:    in.StageCode,
		FaultType:    in.FaultType,
		Description:  in.Description,
		Status:       domain.FaultOpen,
		StartedAt:    startedAt,
		RecordedBy:   in.RecordedBy,
		Notes:        in.Notes,
		LotRefs:      in.LotRefs,
	}
	if f.FaultID == "" {
		f.FaultID = newID("FLT")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO equipment_faults (fault_id, equipment_ref, stage_code, fault_type, description,
		                               status, started_at, recorded_by, recorded_at, notes)
		 VALUES (?,?,?,?,?, 'OPEN', ?,?,?,?)`,
		f.FaultID, f.EquipmentRef, f.StageCode, f.FaultType, nullable(f.Description),
		f.StartedAt, nullable(f.RecordedBy), nowText(), nullable(f.Notes)); err != nil {
		if isUniqueViolation(err) {
			return nil, conflictf("故障记录 %s 已存在", f.FaultID)
		}
		return nil, err
	}
	for _, ref := range in.LotRefs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO fault_lots(fault_id, lot_ref) VALUES (?,?)`, f.FaultID, ref); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return f, nil
}

// ResolveFault 标记故障已排除。
func (s *Store) ResolveFault(ctx context.Context, faultID, note string) (*EquipmentFault, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE equipment_faults SET status='RESOLVED', resolved_at=?,
		      notes=COALESCE(NULLIF(?, ''), notes)
		 WHERE fault_id=? AND status='OPEN'`, nowText(), note, faultID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		err := s.db.QueryRowContext(ctx,
			`SELECT status FROM equipment_faults WHERE fault_id=?`, faultID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return nil, conflictf("故障记录 %s 当前状态为 %s，不能重复排除", faultID, status)
	}
	return s.GetFault(ctx, faultID)
}

// GetFault 查询单条故障。
func (s *Store) GetFault(ctx context.Context, faultID string) (*EquipmentFault, error) {
	f := &EquipmentFault{}
	var description, resolvedAt, recordedBy, notes sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT fault_id, equipment_ref, stage_code, fault_type, description, status,
		        started_at, resolved_at, recorded_by, COALESCE(notes,'')
		 FROM equipment_faults WHERE fault_id=?`, faultID).
		Scan(&f.FaultID, &f.EquipmentRef, &f.StageCode, &f.FaultType, &description, &f.Status,
			&f.StartedAt, &resolvedAt, &recordedBy, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.Description = description.String
	f.ResolvedAt = resolvedAt.String
	f.RecordedBy = recordedBy.String
	f.Notes = notes.String
	f.LotRefs, err = s.faultLots(ctx, faultID)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ListFaults 返回故障记录；lotRef 非空时只返回关联该批次的故障。
func (s *Store) ListFaults(ctx context.Context, lotRef string) ([]EquipmentFault, error) {
	q := `SELECT f.fault_id FROM equipment_faults f`
	args := []any{}
	if lotRef != "" {
		q += ` JOIN fault_lots fl ON fl.fault_id = f.fault_id WHERE fl.lot_ref=?`
		args = append(args, lotRef)
	}
	q += ` ORDER BY f.started_at, f.fault_id`
	rows, err := s.db.QueryContext(ctx, q, args...)
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
	out := make([]EquipmentFault, 0, len(ids))
	for _, id := range ids {
		f, err := s.GetFault(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, nil
}

func (s *Store) faultLots(ctx context.Context, faultID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT lot_ref FROM fault_lots WHERE fault_id=? ORDER BY lot_ref`, faultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
