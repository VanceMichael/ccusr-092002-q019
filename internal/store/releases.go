package store

import (
	"context"
	"database/sql"
	"errors"
)

// CreateRelease 登记批次放流（一批一次）。批次必须已到达放流点，
// 且放流数量等于账面存活数——链路中任何未销账短少都会让批次停在前面环节而无法放流。
func (s *Store) CreateRelease(ctx context.Context, in ReleaseInput) (*Release, error) {
	lot, err := s.GetLot(ctx, in.LotRef)
	if err != nil {
		return nil, err
	}
	if lot.Status == "RELEASED" {
		return nil, conflictf("批次 %s 已放流，每批只能放流一次", lot.LotRef)
	}
	if lot.CurrentStage != "RELEASE_SITE" {
		return nil, conflictf("批次 %s 当前位于 %s，必须逐段交接至放流点后才能放流",
			lot.LotRef, lot.CurrentStage)
	}
	if lot.Status != "IN_CHAIN" {
		return nil, conflictf("批次 %s 当前状态为 %s，存在未结清的交接，不能放流", lot.LotRef, lot.Status)
	}
	if in.Waterbody == "" {
		return nil, rulef("waterbody（放流水体）不能为空")
	}
	if in.Count <= 0 {
		return nil, rulef("放流数量必须大于 0")
	}
	bal, err := s.Balance(ctx, lot.LotRef)
	if err != nil {
		return nil, err
	}
	if err := s.guardPendingAdj(ctx, lot.LotRef); err != nil {
		return nil, err
	}
	if in.Count != bal.Available {
		return nil, rulef("放流数量 %d 必须等于账面存活数 %d（初登 %d + 重分拣入 %d - 重分拣出 %d - 确认死亡 %d）",
			in.Count, bal.Available, bal.Initial, bal.ResortedIn, bal.ResortedOut, bal.LossesConfirmed)
	}
	releasedAt, err := parseTime(in.ReleasedAt)
	if err != nil {
		return nil, err
	}
	r := &Release{
		ReleaseID:  newID("REL"),
		LotRef:     lot.LotRef,
		ReleaseRef: in.ReleaseRef,
		Count:      in.Count,
		Waterbody:  in.Waterbody,
		SiteName:   in.SiteName,
		Latitude:   in.Latitude,
		Longitude:  in.Longitude,
		ReleasedAt: releasedAt,
		ReleasedBy: in.ReleasedBy,
		Notes:      in.Notes,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO releases (release_id, lot_ref, release_ref, count, waterbody, site_name,
		                       latitude, longitude, released_at, released_by, notes, recorded_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ReleaseID, r.LotRef, nullable(r.ReleaseRef), r.Count, r.Waterbody, nullable(r.SiteName),
		nullFloat(r.Latitude), nullFloat(r.Longitude), r.ReleasedAt, nullable(r.ReleasedBy),
		nullable(r.Notes), nowText()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE lots SET status='RELEASED' WHERE lot_ref=?`, lot.LotRef); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

// GetRelease 查询批次的放流记录。
func (s *Store) GetRelease(ctx context.Context, lotRef string) (*Release, error) {
	r := &Release{}
	var releaseRef, siteName, releasedBy, notes sql.NullString
	var lat, lon sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT release_id, lot_ref, release_ref, count, waterbody, site_name, latitude, longitude,
		        released_at, released_by, COALESCE(notes,'')
		 FROM releases WHERE lot_ref=?`, lotRef).
		Scan(&r.ReleaseID, &r.LotRef, &releaseRef, &r.Count, &r.Waterbody, &siteName, &lat, &lon,
			&r.ReleasedAt, &releasedBy, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ReleaseRef = releaseRef.String
	r.SiteName = siteName.String
	r.ReleasedBy = releasedBy.String
	r.Notes = notes.String
	if lat.Valid {
		r.Latitude = &lat.Float64
	}
	if lon.Valid {
		r.Longitude = &lon.Float64
	}
	return r, nil
}

// AddMark 登记增殖鱼苗的耳石/荧光等标记，标记编码供后续回捕调查匹配。
func (s *Store) AddMark(ctx context.Context, in MarkInput) (*Mark, error) {
	lot, err := s.GetLot(ctx, in.LotRef)
	if err != nil {
		return nil, err
	}
	if lot.Status == "RELEASED" {
		return nil, conflictf("批次 %s 已放流，标记应在放流前完成", lot.LotRef)
	}
	switch in.MarkType {
	case "OTOLITH", "FLUORESCENT", "PIT", "OTHER":
	default:
		return nil, rulef("mark_type 只能是 OTOLITH / FLUORESCENT / PIT / OTHER")
	}
	if in.MarkCode == "" {
		return nil, rulef("mark_code 不能为空（同批同方法可共用一个批次标记编码）")
	}
	if in.MarkedCount <= 0 {
		return nil, rulef("marked_count 必须大于 0")
	}
	bal, err := s.Balance(ctx, lot.LotRef)
	if err != nil {
		return nil, err
	}
	if in.MarkedCount > bal.Available {
		return nil, rulef("标记数量 %d 超过批次账面存活数 %d", in.MarkedCount, bal.Available)
	}
	markedAt, err := parseTime(in.MarkedAt)
	if err != nil {
		return nil, err
	}
	m := &Mark{
		MarkID:      newID("MK"),
		LotRef:      lot.LotRef,
		MarkType:    in.MarkType,
		MarkCode:    in.MarkCode,
		MarkedCount: in.MarkedCount,
		MarkedAt:    markedAt,
		Method:      in.Method,
		Notes:       in.Notes,
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO marks (mark_id, lot_ref, mark_type, mark_code, marked_count, marked_at,
		                    method, notes, recorded_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		m.MarkID, m.LotRef, m.MarkType, m.MarkCode, m.MarkedCount, m.MarkedAt,
		nullable(m.Method), nullable(m.Notes), nowText()); err != nil {
		if isUniqueViolation(err) {
			return nil, conflictf("标记编码 %s 已存在", m.MarkCode)
		}
		return nil, err
	}
	return m, nil
}

// ListMarks 返回批次的全部标记。
func (s *Store) ListMarks(ctx context.Context, lotRef string) ([]Mark, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT mark_id, lot_ref, mark_type, mark_code, marked_count, marked_at,
		        COALESCE(method,''), COALESCE(notes,'')
		 FROM marks WHERE lot_ref=? ORDER BY marked_at, mark_id`, lotRef)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Mark, 0)
	for rows.Next() {
		var m Mark
		if err := rows.Scan(&m.MarkID, &m.LotRef, &m.MarkType, &m.MarkCode, &m.MarkedCount,
			&m.MarkedAt, &m.Method, &m.Notes); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}
