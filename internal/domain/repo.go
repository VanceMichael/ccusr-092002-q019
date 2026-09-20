package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// querier 由 *sql.DB 与 *sql.Tx 共同实现。
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const handoffColumns = `id, lot_code, stage_code, seq, expected_count, received_count,
       shortage_count, status, reported_by, receiver_by, note, created_at, received_at,
       release_site_code, release_site_name, longitude, latitude, released_at`

type handoffRow struct {
	ID              int64
	LotCode         string
	StageCode       string
	Seq             int
	ExpectedCount   int
	ReceivedCount   *int
	ShortageCount   *int
	Status          string
	ReportedBy      string
	ReceiverBy      string
	Note            string
	CreatedAt       string
	ReceivedAt      string
	ReleaseSiteCode string
	ReleaseSiteName string
	Longitude       *float64
	Latitude        *float64
	ReleasedAt      string
}

func scanHandoffRow(scanner interface {
	Scan(dest ...any) error
}) (*handoffRow, error) {
	var r handoffRow
	var received sql.NullInt64
	var shortage sql.NullInt64
	var receivedAt sql.NullString
	var releasedAt sql.NullString
	var longitude, latitude sql.NullFloat64
	if err := scanner.Scan(&r.ID, &r.LotCode, &r.StageCode, &r.Seq, &r.ExpectedCount,
		&received, &shortage, &r.Status, &r.ReportedBy, &r.ReceiverBy, &r.Note,
		&r.CreatedAt, &receivedAt, &r.ReleaseSiteCode, &r.ReleaseSiteName,
		&longitude, &latitude, &releasedAt); err != nil {
		return nil, err
	}
	if received.Valid {
		v := int(received.Int64)
		r.ReceivedCount = &v
	}
	if shortage.Valid {
		v := int(shortage.Int64)
		r.ShortageCount = &v
	}
	r.ReceivedAt = receivedAt.String
	r.ReleasedAt = releasedAt.String
	if longitude.Valid {
		r.Longitude = &longitude.Float64
	}
	if latitude.Valid {
		r.Latitude = &latitude.Float64
	}
	return &r, nil
}

func (s *Service) handoffByStageTx(ctx context.Context, q querier, lotCode, stageCode string) (*handoffRow, error) {
	row := q.QueryRowContext(ctx,
		"SELECT "+handoffColumns+" FROM handoffs WHERE lot_code = ? AND stage_code = ?",
		lotCode, stageCode)
	h, err := scanHandoffRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 批次 %s 的 %s 交接", ErrNotFound, lotCode, StageNames[stageCode])
	}
	return h, err
}

func (s *Service) handoffByIDTx(ctx context.Context, q querier, id int64) (*handoffRow, error) {
	row := q.QueryRowContext(ctx,
		"SELECT "+handoffColumns+" FROM handoffs WHERE id = ?", id)
	h, err := scanHandoffRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 交接 %d", ErrNotFound, id)
	}
	return h, err
}

func (s *Service) lastHandoffTx(ctx context.Context, q querier, lotCode string) (*handoffRow, error) {
	row := q.QueryRowContext(ctx, `
		SELECT `+handoffColumns+` FROM handoffs WHERE lot_code = ?
		ORDER BY seq DESC LIMIT 1`, lotCode)
	h, err := scanHandoffRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Service) containerExpectedTx(ctx context.Context, q querier, handoffID int64) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT container_ref, expected_count FROM handoff_containers WHERE handoff_id = ?`,
		handoffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var ref string
		var count int
		if err := rows.Scan(&ref, &count); err != nil {
			return nil, err
		}
		result[ref] = count
	}
	return result, rows.Err()
}

func (s *Service) containersTx(ctx context.Context, q querier, handoffID int64) ([]ContainerItem, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT container_ref, expected_count, received_count
		FROM handoff_containers WHERE handoff_id = ? ORDER BY id`, handoffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ContainerItem
	for rows.Next() {
		var item ContainerItem
		var received sql.NullInt64
		if err := rows.Scan(&item.ContainerRef, &item.ExpectedCount, &received); err != nil {
			return nil, err
		}
		if received.Valid {
			v := int(received.Int64)
			item.ReceivedCount = &v
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) handoffView(ctx context.Context, id int64) (*HandoffView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.handoffViewTx(ctx, tx, id)
}

func (s *Service) handoffViewTx(ctx context.Context, q querier, id int64) (*HandoffView, error) {
	row, err := s.handoffByIDTx(ctx, q, id)
	if err != nil {
		return nil, err
	}
	view := &HandoffView{
		ID:              row.ID,
		LotCode:         row.LotCode,
		StageCode:       row.StageCode,
		StageName:       StageNames[row.StageCode],
		Seq:             row.Seq,
		ExpectedCount:   row.ExpectedCount,
		ReceivedCount:   row.ReceivedCount,
		ShortageCount:   row.ShortageCount,
		Status:          row.Status,
		ReportedBy:      row.ReportedBy,
		ReceiverBy:      row.ReceiverBy,
		Note:            row.Note,
		CreatedAt:       row.CreatedAt,
		ReceivedAt:      row.ReceivedAt,
		Balanced:        row.Status == StatusBalanced,
		ReleaseSiteCode: row.ReleaseSiteCode,
		ReleaseSiteName: row.ReleaseSiteName,
		Longitude:       row.Longitude,
		Latitude:        row.Latitude,
		ReleasedAt:      row.ReleasedAt,
	}
	if view.Containers, err = s.containersTx(ctx, q, id); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0)
		FROM loss_events WHERE handoff_id = ?`,
		ClaimConfirmed, ClaimPending, id).Scan(&view.ConfirmedLoss, &view.PendingLoss); err != nil {
		return nil, err
	}
	return view, nil
}

// GetHandoff 按批次与环节查询交接守恒结果。
func (s *Service) GetHandoff(ctx context.Context, lotCode, stageCode string) (*HandoffView, error) {
	if stageIndex(stageCode) < 0 {
		return nil, fmt.Errorf("%w: 环节编码非法", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := s.handoffByStageTx(ctx, tx, lotCode, stageCode)
	if err != nil {
		return nil, err
	}
	return s.handoffViewTx(ctx, tx, row.ID)
}

const lossColumns = `id, handoff_id, lot_code, stage_code, kind, count, to_lot_code,
       reason, evidence_ref, status, reported_by, confirmed_by, created_at, confirmed_at`

func scanLossEvent(scanner interface{ Scan(dest ...any) error }) (*LossEventView, error) {
	var v LossEventView
	var toLot sql.NullString
	var confirmedAt sql.NullString
	if err := scanner.Scan(&v.ID, &v.HandoffID, &v.LotCode, &v.StageCode, &v.Kind,
		&v.Count, &toLot, &v.Reason, &v.EvidenceRef, &v.Status, &v.ReportedBy,
		&v.ConfirmedBy, &v.CreatedAt, &confirmedAt); err != nil {
		return nil, err
	}
	v.ToLotCode = toLot.String
	v.ConfirmedAt = confirmedAt.String
	return &v, nil
}

func (s *Service) lossEventTx(ctx context.Context, q querier, id int64) (*LossEventView, error) {
	row := q.QueryRowContext(ctx,
		"SELECT "+lossColumns+" FROM loss_events WHERE id = ?", id)
	v, err := scanLossEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 凭证 %d", ErrNotFound, id)
	}
	return v, err
}

func (s *Service) lossEventView(ctx context.Context, id int64) (*LossEventView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.lossEventTx(ctx, tx, id)
}

func (s *Service) failureView(ctx context.Context, id int64) (*EquipmentFailureView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, lot_code, stage_code, equipment_code, description,
		       downtime_minutes, occurred_at, reported_by, created_at
		FROM equipment_failures WHERE id = ?`, id)
	var v EquipmentFailureView
	var lot sql.NullString
	var downtime sql.NullInt64
	if err := row.Scan(&v.ID, &lot, &v.StageCode, &v.EquipmentCode, &v.Description,
		&downtime, &v.OccurredAt, &v.ReportedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	v.LotCode = lot.String
	v.StageName = StageNames[v.StageCode]
	if downtime.Valid {
		d := int(downtime.Int64)
		v.DowntimeMinutes = &d
	}
	return &v, nil
}

func (s *Service) markView(ctx context.Context, id int64) (*MarkView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, lot_code, mark_type, marker_code, marked_count, marked_at,
		       recorded_by, created_at
		FROM marks WHERE id = ?`, id)
	return scanMark(row)
}

func scanMarks(rows *sql.Rows) ([]MarkView, error) {
	views := []MarkView{}
	for rows.Next() {
		view, err := scanMark(rows)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, rows.Err()
}

func scanMark(scanner interface{ Scan(dest ...any) error }) (*MarkView, error) {
	var v MarkView
	if err := scanner.Scan(&v.ID, &v.LotCode, &v.MarkType, &v.MarkerCode, &v.MarkedCount,
		&v.MarkedAt, &v.RecordedBy, &v.CreatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

const recaptureSelect = `SELECT id, survey_code, species_code, count, mark_type, marker_code,
       origin_result, matched_lot_code, evidence_ref, created_at `

func scanRecapture(scanner interface{ Scan(dest ...any) error }) (*RecaptureView, error) {
	var v RecaptureView
	var markType sql.NullString
	var matched sql.NullString
	if err := scanner.Scan(&v.ID, &v.SurveyCode, &v.SpeciesCode, &v.Count,
		&markType, &v.MarkerCode, &v.OriginResult, &matched, &v.EvidenceRef,
		&v.CreatedAt); err != nil {
		return nil, err
	}
	v.MarkType = markType.String
	v.MatchedLotCode = matched.String
	return &v, nil
}

func (s *Service) recaptureView(ctx context.Context, id int64) (*RecaptureView, error) {
	row := s.db.QueryRowContext(ctx,
		recaptureSelect+"FROM recaptures WHERE id = ?", id)
	return scanRecapture(row)
}

// ListLossEvents 列出批次的全部短数凭证（含待确认与已驳回）。
func (s *Service) ListLossEvents(ctx context.Context, lotCode string) ([]LossEventView, error) {
	if _, err := s.GetLot(ctx, lotCode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+lossColumns+" FROM loss_events WHERE lot_code = ? ORDER BY id", lotCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []LossEventView
	for rows.Next() {
		v, err := scanLossEvent(rows)
		if err != nil {
			return nil, err
		}
		views = append(views, *v)
	}
	return views, rows.Err()
}

// ListFailures 列出与批次相关的设备故障。
func (s *Service) ListFailures(ctx context.Context, lotCode string) ([]EquipmentFailureView, error) {
	if _, err := s.GetLot(ctx, lotCode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, lot_code, stage_code, equipment_code, description,
		       downtime_minutes, occurred_at, reported_by, created_at
		FROM equipment_failures WHERE lot_code = ? ORDER BY occurred_at, id`, lotCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []EquipmentFailureView
	for rows.Next() {
		var v EquipmentFailureView
		var lot sql.NullString
		var downtime sql.NullInt64
		if err := rows.Scan(&v.ID, &lot, &v.StageCode, &v.EquipmentCode, &v.Description,
			&downtime, &v.OccurredAt, &v.ReportedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.LotCode = lot.String
		v.StageName = StageNames[v.StageCode]
		if downtime.Valid {
			d := int(downtime.Int64)
			v.DowntimeMinutes = &d
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

// Trace 汇总管理部门查询一批鱼所需的全链路视图：每次交接的守恒结果、
// 放流落点，以及数月后与本批增殖标记关联的监测证据。
func (s *Service) Trace(ctx context.Context, lotCode string) (*LotTrace, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	lot, err := s.getLotTx(ctx, tx, lotCode)
	if err != nil {
		return nil, err
	}
	trace := &LotTrace{
		Lot:               *lot,
		Handoffs:          []HandoffView{},
		LossEvents:        []LossEventView{},
		Adjustments:       []AdjustmentView{},
		EquipmentFailures: []EquipmentFailureView{},
		Marks:             []MarkView{},
	}

	hRows, err := tx.QueryContext(ctx,
		"SELECT "+handoffColumns+" FROM handoffs WHERE lot_code = ? ORDER BY seq", lotCode)
	if err != nil {
		return nil, err
	}
	for hRows.Next() {
		row, scanErr := scanHandoffRow(hRows)
		if scanErr != nil {
			hRows.Close()
			return nil, scanErr
		}
		view := HandoffView{
			ID: row.ID, LotCode: row.LotCode, StageCode: row.StageCode,
			StageName: StageNames[row.StageCode], Seq: row.Seq,
			ExpectedCount: row.ExpectedCount, ReceivedCount: row.ReceivedCount,
			ShortageCount: row.ShortageCount, Status: row.Status,
			ReportedBy: row.ReportedBy, ReceiverBy: row.ReceiverBy, Note: row.Note,
			CreatedAt: row.CreatedAt, ReceivedAt: row.ReceivedAt,
			Balanced:        row.Status == StatusBalanced,
			ReleaseSiteCode: row.ReleaseSiteCode, ReleaseSiteName: row.ReleaseSiteName,
			Longitude: row.Longitude, Latitude: row.Latitude, ReleasedAt: row.ReleasedAt,
		}
		if view.Containers, err = s.containersTx(ctx, tx, row.ID); err != nil {
			hRows.Close()
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0),
			       COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0)
			FROM loss_events WHERE handoff_id = ?`,
			ClaimConfirmed, ClaimPending, row.ID).Scan(&view.ConfirmedLoss, &view.PendingLoss); err != nil {
			hRows.Close()
			return nil, err
		}
		trace.Handoffs = append(trace.Handoffs, view)
	}
	hRows.Close()
	if err := hRows.Err(); err != nil {
		return nil, err
	}

	lRows, err := tx.QueryContext(ctx,
		"SELECT "+lossColumns+" FROM loss_events WHERE lot_code = ? ORDER BY id", lotCode)
	if err != nil {
		return nil, err
	}
	for lRows.Next() {
		v, scanErr := scanLossEvent(lRows)
		if scanErr != nil {
			lRows.Close()
			return nil, scanErr
		}
		trace.LossEvents = append(trace.LossEvents, *v)
	}
	lRows.Close()
	if err := lRows.Err(); err != nil {
		return nil, err
	}

	aRows, err := tx.QueryContext(ctx, `
		SELECT id, lot_code, delta, reason_kind, ref_loss_event_id, stage_code, created_at
		FROM lot_adjustments WHERE lot_code = ? ORDER BY id`, lotCode)
	if err != nil {
		return nil, err
	}
	for aRows.Next() {
		var a AdjustmentView
		if err := aRows.Scan(&a.ID, &a.LotCode, &a.Delta, &a.ReasonKind,
			&a.RefLossEventID, &a.StageCode, &a.CreatedAt); err != nil {
			aRows.Close()
			return nil, err
		}
		trace.Adjustments = append(trace.Adjustments, a)
	}
	aRows.Close()
	if err := aRows.Err(); err != nil {
		return nil, err
	}

	fRows, err := tx.QueryContext(ctx, `
		SELECT id, lot_code, stage_code, equipment_code, description,
		       downtime_minutes, occurred_at, reported_by, created_at
		FROM equipment_failures WHERE lot_code = ? ORDER BY occurred_at, id`, lotCode)
	if err != nil {
		return nil, err
	}
	for fRows.Next() {
		var v EquipmentFailureView
		var lotRef sql.NullString
		var downtime sql.NullInt64
		if err := fRows.Scan(&v.ID, &lotRef, &v.StageCode, &v.EquipmentCode, &v.Description,
			&downtime, &v.OccurredAt, &v.ReportedBy, &v.CreatedAt); err != nil {
			fRows.Close()
			return nil, err
		}
		v.LotCode = lotRef.String
		v.StageName = StageNames[v.StageCode]
		if downtime.Valid {
			d := int(downtime.Int64)
			v.DowntimeMinutes = &d
		}
		trace.EquipmentFailures = append(trace.EquipmentFailures, v)
	}
	fRows.Close()
	if err := fRows.Err(); err != nil {
		return nil, err
	}

	mRows, err := tx.QueryContext(ctx, `
		SELECT id, lot_code, mark_type, marker_code, marked_count, marked_at,
		       recorded_by, created_at
		FROM marks WHERE lot_code = ? ORDER BY id`, lotCode)
	if err != nil {
		return nil, err
	}
	trace.Marks, err = scanMarks(mRows)
	if err != nil {
		return nil, err
	}

	summary, err := s.buildConservationTx(ctx, tx, lot)
	if err != nil {
		return nil, err
	}
	trace.Conservation = *summary
	for i := range trace.Handoffs {
		h := trace.Handoffs[i]
		if h.StageCode == StageRelease && h.ReleasedAt != "" {
			count := 0
			if h.ReceivedCount != nil {
				count = *h.ReceivedCount
			}
			trace.Release = &ReleaseSummary{
				SiteCode:   h.ReleaseSiteCode,
				SiteName:   h.ReleaseSiteName,
				Count:      count,
				Longitude:  h.Longitude,
				Latitude:   h.Latitude,
				ReleasedAt: h.ReleasedAt,
			}
		}
	}
	trace.Monitoring = s.buildMonitoringTx(ctx, tx, lot)
	return trace, nil
}

func (s *Service) buildConservationTx(ctx context.Context, q querier, lot *Lot) (*ConservationSummary, error) {
	summary := &ConservationSummary{InitialCount: lot.InitialCount, Balanced: true}
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN delta > 0 THEN delta ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN delta < 0 THEN -delta ELSE 0 END),0)
		FROM lot_adjustments WHERE lot_code = ?`, lot.LotCode).
		Scan(&summary.ResortIn, &summary.ResortOut); err != nil {
		return nil, err
	}
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN status = ? THEN count ELSE 0 END),0)
		FROM loss_events WHERE lot_code = ? AND kind = ?`,
		ClaimConfirmed, ClaimPending, lot.LotCode, LossDeath).
		Scan(&summary.ConfirmedDeaths, &summary.PendingDeathClaims); err != nil {
		return nil, err
	}
	summary.AccountedCount = summary.InitialCount + summary.ResortIn -
		summary.ResortOut - summary.ConfirmedDeaths

	handoffCount := 0
	if err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM handoffs WHERE lot_code = ?", lot.LotCode).Scan(&handoffCount); err != nil {
		return nil, err
	}
	var blocked int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM handoffs WHERE lot_code = ? AND status <> ?`,
		lot.LotCode, StatusBalanced).Scan(&blocked); err != nil {
		return nil, err
	}
	var pendingClaims int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM loss_events WHERE lot_code = ? AND status = ?`,
		lot.LotCode, ClaimPending).Scan(&pendingClaims); err != nil {
		return nil, err
	}
	summary.Balanced = handoffCount > 0 && blocked == 0 && pendingClaims == 0

	var released sql.NullInt64
	if err := q.QueryRowContext(ctx, `
		SELECT received_count FROM handoffs
		WHERE lot_code = ? AND stage_code = ? AND released_at <> ''`,
		lot.LotCode, StageRelease).Scan(&released); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if released.Valid {
		summary.ReleasedCount = int(released.Int64)
	}
	return summary, nil
}

// buildMonitoringTx 汇总与本批相关的监测证据。
// 只选取“出现过本批标记命中”的调查；成效命中严格只计 matched_lot_code 为本批的尾数，
// 同场调查中的无标记野生鱼与有标无录仅作并列展示，绝不并入成效。
func (s *Service) buildMonitoringTx(ctx context.Context, q querier, lot *Lot) MonitoringSummary {
	summary := MonitoringSummary{Surveys: []SurveyRecap{}}
	surveyRows, err := q.QueryContext(ctx, `
		SELECT DISTINCT sv.survey_code, sv.survey_date, sv.site_name
		FROM recaptures r
		JOIN surveys sv ON sv.survey_code = r.survey_code
		WHERE r.matched_lot_code = ?
		ORDER BY sv.survey_date, sv.survey_code`, lot.LotCode)
	if err != nil {
		return summary
	}
	type surveyKey struct {
		code, date, site string
	}
	var keys []surveyKey
	for surveyRows.Next() {
		var k surveyKey
		if err := surveyRows.Scan(&k.code, &k.date, &k.site); err != nil {
			surveyRows.Close()
			return emptyMonitoring()
		}
		keys = append(keys, k)
	}
	surveyRows.Close()
	if err := surveyRows.Err(); err != nil {
		return emptyMonitoring()
	}

	for _, k := range keys {
		recap := SurveyRecap{SurveyCode: k.code, SurveyDate: k.date, SiteName: k.site}
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(count),0) FROM recaptures
			WHERE survey_code = ? AND matched_lot_code = ? AND origin_result = ?`,
			k.code, lot.LotCode, ResultCredited).Scan(&recap.Credited); err != nil {
			return emptyMonitoring()
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(count),0) FROM recaptures
			WHERE survey_code = ? AND origin_result = ? AND species_code = ?
			  AND matched_lot_code IS NULL`,
			k.code, ResultWild, lot.SpeciesCode).Scan(&recap.Wild); err != nil {
			return emptyMonitoring()
		}
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(count),0) FROM recaptures
			WHERE survey_code = ? AND origin_result = ? AND species_code = ?
			  AND matched_lot_code IS NULL`,
			k.code, ResultUnmatchedMark, lot.SpeciesCode).Scan(&recap.Unmatched); err != nil {
			return emptyMonitoring()
		}
		summary.CreditedRecaptures += recap.Credited
		summary.WildRecaptures += recap.Wild
		summary.UnmatchedMarks += recap.Unmatched
		summary.Surveys = append(summary.Surveys, recap)
	}
	return summary
}

func emptyMonitoring() MonitoringSummary { return MonitoringSummary{Surveys: []SurveyRecap{}} }
