package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vancemichael/092002-dam-fish-passage/internal/domain"
)

// CreateLot 登记一个进入过坝链路的鱼批次。
func (s *Store) CreateLot(ctx context.Context, in CreateLotInput) (*Lot, error) {
	in.LotRef = strings.TrimSpace(in.LotRef)
	in.SpeciesCode = strings.TrimSpace(in.SpeciesCode)
	if in.LotRef == "" {
		return nil, rulef("lot_ref 不能为空")
	}
	if in.SpeciesCode == "" {
		return nil, rulef("species_code 不能为空")
	}
	if in.Count <= 0 {
		return nil, rulef("初始登记数量必须大于 0")
	}
	origin := in.Origin
	if origin == "" {
		origin = domain.OriginWild
	}
	if origin != domain.OriginWild && origin != domain.OriginHatchery {
		return nil, rulef("origin 只能是 WILD 或 HATCHERY")
	}
	entry := in.EntryStage
	if entry == "" {
		entry = string(domain.StageCollection)
	}
	if !domain.ValidStage(entry) {
		return nil, rulef("entry_stage 不合法: %s", entry)
	}
	createdAt, err := parseTime(in.OccurredAt)
	if err != nil {
		return nil, err
	}

	lot := &Lot{
		LotRef:       in.LotRef,
		SpeciesCode:  in.SpeciesCode,
		Origin:       origin,
		InitialCount: in.Count,
		EntryStage:   entry,
		CurrentStage: entry,
		Status:       "IN_CHAIN",
		Notes:        in.Notes,
		CreatedAt:    createdAt,
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO lots (lot_ref, species_code, origin, initial_count, entry_stage,
		                   current_stage, status, notes, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		lot.LotRef, lot.SpeciesCode, lot.Origin, lot.InitialCount, lot.EntryStage,
		lot.CurrentStage, lot.Status, nullable(lot.Notes), lot.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, conflictf("批次 %s 已存在", lot.LotRef)
		}
		return nil, err
	}
	return lot, nil
}

// GetLot 查询单个批次。
func (s *Store) GetLot(ctx context.Context, lotRef string) (*Lot, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT lot_ref, species_code, origin, initial_count, entry_stage,
		        current_stage, status, COALESCE(notes,''), created_at
		 FROM lots WHERE lot_ref = ?`, lotRef)
	lot := &Lot{}
	if err := row.Scan(&lot.LotRef, &lot.SpeciesCode, &lot.Origin, &lot.InitialCount,
		&lot.EntryStage, &lot.CurrentStage, &lot.Status, &lot.Notes, &lot.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return lot, nil
}

// ListLots 按创建顺序返回全部批次，origin 为空时不过滤。
func (s *Store) ListLots(ctx context.Context, origin string) ([]Lot, error) {
	q := `SELECT lot_ref, species_code, origin, initial_count, entry_stage,
	             current_stage, status, COALESCE(notes,''), created_at
	      FROM lots`
	args := []any{}
	if origin != "" {
		if origin != domain.OriginWild && origin != domain.OriginHatchery {
			return nil, rulef("origin 只能是 WILD 或 HATCHERY")
		}
		q += " WHERE origin = ?"
		args = append(args, origin)
	}
	q += " ORDER BY created_at, lot_ref"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Lot, 0)
	for rows.Next() {
		var l Lot
		if err := rows.Scan(&l.LotRef, &l.SpeciesCode, &l.Origin, &l.InitialCount,
			&l.EntryStage, &l.CurrentStage, &l.Status, &l.Notes, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LotBalance 是一个批次在链路中的守恒账面。
type LotBalance struct {
	Initial         int `json:"initial"`
	ResortedIn      int `json:"resorted_in"`
	ResortedOut     int `json:"resorted_out"`
	Available       int `json:"available"`        // 当前账面上仍存活的鱼数
	LossesConfirmed int `json:"losses_confirmed"` // 经确认死亡
	Released        int `json:"released"`
}

// Balance 汇总批次的守恒账面，所有数字只统计 CONFIRMED 的交接/死亡/重分拣。
func (s *Store) Balance(ctx context.Context, lotRef string) (LotBalance, error) {
	var b LotBalance
	if _, err := s.GetLot(ctx, lotRef); err != nil {
		return b, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(initial_count,0) FROM lots WHERE lot_ref=?`, lotRef).Scan(&b.Initial); err != nil {
		return b, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM resort_events WHERE to_lot_ref=? AND status='CONFIRMED'`,
		lotRef).Scan(&b.ResortedIn); err != nil {
		return b, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM resort_events WHERE from_lot_ref=? AND status='CONFIRMED'`,
		lotRef).Scan(&b.ResortedOut); err != nil {
		return b, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM loss_events WHERE lot_ref=? AND status='CONFIRMED'`,
		lotRef).Scan(&b.LossesConfirmed); err != nil {
		return b, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(count),0) FROM releases WHERE lot_ref=?`, lotRef).
		Scan(&b.Released); err != nil {
		return b, err
	}
	// 可用 = 入量 - 出量；在途（SENT/DISPUTED）交接不扣减鱼数，差额由死亡/重分拣确认时销账。
	b.Available = b.Initial + b.ResortedIn - b.ResortedOut - b.LossesConfirmed - b.Released
	return b, nil
}

// guardPendingAdj 要求批次没有待确认的站点级死亡/重分拣记录——
// 这类记录必须先确认或驳回，批次才能继续发出或放流。
func (s *Store) guardPendingAdj(ctx context.Context, lotRef string) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM loss_events
		 WHERE lot_ref=? AND status='PENDING' AND handover_id IS NULL`, lotRef).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return rulef("批次 %s 有 %d 条待确认的死亡记录，请确认或驳回后再继续流转", lotRef, n)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM resort_events
		 WHERE from_lot_ref=? AND status='PENDING' AND handover_id IS NULL`, lotRef).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return rulef("批次 %s 有 %d 条待确认的重新分拣记录，请确认或驳回后再继续流转", lotRef, n)
	}
	return nil
}
