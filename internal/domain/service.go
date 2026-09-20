package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vancemichael/092002-dam-fish-passage/internal/store"
)

// Service 承载全部业务规则，所有写操作在单个数据库事务内完成，
// 保证交接清点、短数凭证与批次台账同时生效或同时回滚。
type Service struct {
	db *store.DB
}

// NewService 创建领域服务。
func NewService(db *store.DB) *Service {
	return &Service{db: db}
}

// CreateLot 登记一批鱼。野生与增殖必须显式区分 origin；
// 重新分拣并入产生的新批次以 start_stage 指定进入链路的环节、initial_count 可为 0。
func (s *Service) CreateLot(ctx context.Context, req CreateLotRequest) (*Lot, error) {
	req.LotCode = strings.TrimSpace(req.LotCode)
	req.SpeciesCode = strings.TrimSpace(req.SpeciesCode)
	if req.LotCode == "" || req.SpeciesCode == "" {
		return nil, fmt.Errorf("%w: lot_code 与 species_code 必填", ErrValidation)
	}
	if req.Origin != OriginWild && req.Origin != OriginHatchery {
		return nil, fmt.Errorf("%w: origin 必须为 WILD 或 HATCHERY", ErrValidation)
	}
	if req.InitialCount < 0 {
		return nil, fmt.Errorf("%w: initial_count 不能为负", ErrValidation)
	}
	start := req.StartStage
	if start == "" {
		start = StageSorting
	}
	if stageIndex(start) < 0 {
		return nil, fmt.Errorf("%w: start_stage 不是有效环节", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := s.getLotTx(ctx, tx, req.LotCode); err == nil {
		return nil, fmt.Errorf("%w: 批次 %s 已存在", ErrAlreadyExists, req.LotCode)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	createdAt := nowISO()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lots(lot_code, species_code, origin, source, initial_count,
		                 start_stage, status, note, recorded_by, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		req.LotCode, req.SpeciesCode, req.Origin, req.Source, req.InitialCount,
		start, LotActive, req.Note, req.RecordedBy, createdAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetLot(ctx, req.LotCode)
}

// GetLot 查询单个批次。
func (s *Service) GetLot(ctx context.Context, lotCode string) (*Lot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.getLotTx(ctx, tx, lotCode)
}

func (s *Service) getLotTx(ctx context.Context, q querier, lotCode string) (*Lot, error) {
	row := q.QueryRowContext(ctx, `
		SELECT lot_code, species_code, origin, source, initial_count,
		       start_stage, status, note, recorded_by, created_at
		FROM lots WHERE lot_code = ?`, lotCode)
	lot := &Lot{}
	err := row.Scan(&lot.LotCode, &lot.SpeciesCode, &lot.Origin, &lot.Source,
		&lot.InitialCount, &lot.StartStage, &lot.Status, &lot.Note, &lot.RecordedBy, &lot.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 批次 %s", ErrNotFound, lotCode)
	}
	if err != nil {
		return nil, err
	}
	return lot, nil
}

// ListLots 列出批次，可按 origin/species_code 过滤。
func (s *Service) ListLots(ctx context.Context, origin, species string) ([]Lot, error) {
	if origin != "" && origin != OriginWild && origin != OriginHatchery {
		return nil, fmt.Errorf("%w: origin 过滤值非法", ErrValidation)
	}
	query := `SELECT lot_code, species_code, origin, source, initial_count,
	                    start_stage, status, note, recorded_by, created_at
	             FROM lots WHERE 1=1`
	args := []any{}
	if origin != "" {
		query += " AND origin = ?"
		args = append(args, origin)
	}
	if species != "" {
		query += " AND species_code = ?"
		args = append(args, species)
	}
	query += " ORDER BY created_at, lot_code"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lots []Lot
	for rows.Next() {
		var lot Lot
		if err := rows.Scan(&lot.LotCode, &lot.SpeciesCode, &lot.Origin, &lot.Source,
			&lot.InitialCount, &lot.StartStage, &lot.Status, &lot.Note,
			&lot.RecordedBy, &lot.CreatedAt); err != nil {
			return nil, err
		}
		lots = append(lots, lot)
	}
	return lots, rows.Err()
}

// Dispatch 登记某环节的发出与运输箱清单。expected 由链路守恒账自动算出，
// 运输箱逐箱数量之和必须与其完全一致，防止无记录的增减。
func (s *Service) Dispatch(ctx context.Context, lotCode string, req DispatchRequest) (*HandoffView, error) {
	if stageIndex(req.StageCode) < 0 {
		return nil, fmt.Errorf("%w: transfer_stage 非法", ErrValidation)
	}
	if len(req.Containers) == 0 {
		return nil, fmt.Errorf("%w: 至少登记一个运输箱", ErrValidation)
	}
	containerTotal := 0
	seen := map[string]bool{}
	for _, container := range req.Containers {
		ref := strings.TrimSpace(container.ContainerRef)
		if ref == "" {
			return nil, fmt.Errorf("%w: container_ref 不能为空", ErrValidation)
		}
		if seen[ref] {
			return nil, fmt.Errorf("%w: 运输箱 %s 重复登记", ErrValidation, ref)
		}
		seen[ref] = true
		if container.ExpectedCount < 0 {
			return nil, fmt.Errorf("%w: 运输箱 %s 数量不能为负", ErrValidation, ref)
		}
		containerTotal += container.ExpectedCount
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	lot, err := s.getLotTx(ctx, tx, lotCode)
	if err != nil {
		return nil, err
	}
	if lot.Status != LotActive {
		return nil, fmt.Errorf("%w: 批次已结束，不能再登记交接", ErrConflict)
	}
	if _, err := s.handoffByStageTx(ctx, tx, lotCode, req.StageCode); err == nil {
		return nil, fmt.Errorf("%w: 批次在 %s 环节的交接已存在", ErrAlreadyExists, StageNames[req.StageCode])
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	last, err := s.lastHandoffTx(ctx, tx, lotCode)
	if err != nil {
		return nil, err
	}
	var seq int
	var expected int
	if last == nil {
		if req.StageCode != lot.StartStage {
			return nil, fmt.Errorf("%w: 批次从 %s 入链，首次交接必须是该环节",
				ErrStageOutOfOrder, StageNames[lot.StartStage])
		}
		seq = 1
		expected = lot.InitialCount
	} else {
		if stageIndex(req.StageCode) != stageIndex(last.StageCode)+1 {
			return nil, fmt.Errorf("%w: %s 之后只能登记 %s",
				ErrStageOutOfOrder, StageNames[last.StageCode],
				StageNames[StageOrder[stageIndex(last.StageCode)+1]])
		}
		if last.Status != StatusBalanced || last.ReceivedCount == nil {
			return nil, fmt.Errorf("%w: %s 环节尚未配平",
				ErrPriorUnbalanced, StageNames[last.StageCode])
		}
		seq = last.Seq + 1
		expected = *last.ReceivedCount
	}
	// 同环节已确认的重分拣并入量加入本批发出基数。
	resortIn, err := s.adjustedInTx(ctx, tx, lotCode, req.StageCode)
	if err != nil {
		return nil, err
	}
	expected += resortIn

	if containerTotal != expected {
		return nil, fmt.Errorf("%w: 运输箱清单合计 %d 与守恒应发数 %d 不符",
			ErrValidation, containerTotal, expected)
	}

	createdAt := nowISO()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO handoffs(lot_code, stage_code, seq, expected_count, status,
		                     reported_by, note, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		lotCode, req.StageCode, seq, expected, StatusDispatched, req.ReportedBy, req.Note, createdAt)
	if err != nil {
		return nil, err
	}
	handoffID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, container := range req.Containers {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO handoff_containers(handoff_id, container_ref, expected_count)
			VALUES (?,?,?)`, handoffID, strings.TrimSpace(container.ContainerRef),
			container.ExpectedCount); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.handoffView(ctx, handoffID)
}

// Receive 登记接收方逐箱清点。未逐箱列出或合计超过发出数都会被拒绝；
// 出现短数时交接进入 BLOCKED，必须凭经确认的死亡或重分拣记录才能配平。
func (s *Service) Receive(ctx context.Context, lotCode, stageCode string, req ReceiveRequest) (*HandoffView, error) {
	if stageIndex(stageCode) < 0 {
		return nil, fmt.Errorf("%w: 环节编码非法", ErrValidation)
	}
	if len(req.Containers) == 0 {
		return nil, fmt.Errorf("%w: 必须逐箱上报清点结果", ErrValidation)
	}
	receivedByRef := map[string]int{}
	for _, container := range req.Containers {
		ref := strings.TrimSpace(container.ContainerRef)
		if ref == "" {
			return nil, fmt.Errorf("%w: container_ref 不能为空", ErrValidation)
		}
		if _, dup := receivedByRef[ref]; dup {
			return nil, fmt.Errorf("%w: 运输箱 %s 重复清点", ErrValidation, ref)
		}
		if container.ExpectedCount != 0 { // 接收上报只认 received_count
			return nil, fmt.Errorf("%w: 接收清点不应填写 expected_count", ErrValidation)
		}
		if container.ReceivedCount == nil || *container.ReceivedCount < 0 {
			return nil, fmt.Errorf("%w: 运输箱 %s 的 received_count 必填且非负", ErrValidation, ref)
		}
		receivedByRef[ref] = *container.ReceivedCount
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	handoff, err := s.handoffByStageTx(ctx, tx, lotCode, stageCode)
	if err != nil {
		return nil, err
	}
	if handoff.Status == StatusBalanced {
		return nil, fmt.Errorf("%w: 该环节已配平，清点记录不可更改", ErrConflict)
	}
	expectedRefs, err := s.containerExpectedTx(ctx, tx, handoff.ID)
	if err != nil {
		return nil, err
	}
	if len(receivedByRef) != len(expectedRefs) {
		return nil, fmt.Errorf("%w: 清点箱数 %d 与发出箱数 %d 不一致",
			ErrValidation, len(receivedByRef), len(expectedRefs))
	}
	total := 0
	for ref, count := range receivedByRef {
		expected, ok := expectedRefs[ref]
		if !ok {
			return nil, fmt.Errorf("%w: 运输箱 %s 不在发出清单中", ErrValidation, ref)
		}
		if count > expected {
			return nil, fmt.Errorf("%w: 运输箱 %s 实收 %d 多于发出 %d",
				ErrValidation, ref, count, expected)
		}
		total += count
	}
	if total > handoff.ExpectedCount {
		return nil, fmt.Errorf("%w: 实收总数 %d 多于发出总数 %d",
			ErrValidation, total, handoff.ExpectedCount)
	}

	receivedAt, err := parseTimeOrDefault(req.ReceivedAt, nowISO())
	if err != nil {
		return nil, err
	}
	for ref, count := range receivedByRef {
		if _, err := tx.ExecContext(ctx, `
			UPDATE handoff_containers SET received_count = ?
			WHERE handoff_id = ? AND container_ref = ?`, count, handoff.ID, ref); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE handoffs SET received_count = ?, received_at = ?, receiver_by = ?
		WHERE id = ?`, total, receivedAt, req.ReceiverBy, handoff.ID); err != nil {
		return nil, err
	}
	view, err := s.recomputeTx(ctx, tx, handoff.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return view, nil
}

// ReportLoss 上报死亡或重新分拣凭证。凭证初始为 PENDING，不具备守恒效力；
// 重新分拣必须指定并入的目标批次，且目标批次尚未经过该环节。
func (s *Service) ReportLoss(ctx context.Context, lotCode, stageCode string, req LossRequest) (*LossEventView, error) {
	if req.Kind != LossDeath && req.Kind != LossResort {
		return nil, fmt.Errorf("%w: kind 必须为 DEATH 或 RESORT", ErrValidation)
	}
	if req.Count <= 0 {
		return nil, fmt.Errorf("%w: count 必须为正", ErrValidation)
	}
	req.ToLotCode = strings.TrimSpace(req.ToLotCode)
	if req.Kind == LossResort && req.ToLotCode == "" {
		return nil, fmt.Errorf("%w: 重新分拣必须填写 to_lot_code", ErrValidation)
	}
	if req.Kind == LossResort && req.ToLotCode == lotCode {
		return nil, fmt.Errorf("%w: 重新分拣不能并入本批次", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	handoff, err := s.handoffByStageTx(ctx, tx, lotCode, stageCode)
	if err != nil {
		return nil, err
	}
	if handoff.ReceivedCount == nil {
		return nil, fmt.Errorf("%w: 尚未接收清点，无法登记短数凭证", ErrConflict)
	}
	shortage := handoff.ExpectedCount - *handoff.ReceivedCount
	if shortage <= 0 {
		return nil, fmt.Errorf("%w: 本环节清点相符，没有待解释的短数", ErrValidation)
	}
	claimed, err := s.claimedCountTx(ctx, tx, handoff.ID, false)
	if err != nil {
		return nil, err
	}
	if claimed+req.Count > shortage {
		return nil, fmt.Errorf("%w: 本环节短数 %d，已登记凭证 %d，本次 %d 超出",
			ErrClaimOvercount, shortage, claimed, req.Count)
	}
	if req.Kind == LossResort {
		target, err := s.getLotTx(ctx, tx, req.ToLotCode)
		if err != nil {
			return nil, err
		}
		if err := s.targetCanAcceptAtTx(ctx, tx, target, stageCode); err != nil {
			return nil, err
		}
	}

	createdAt := nowISO()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO loss_events(handoff_id, lot_code, stage_code, kind, count,
		                        to_lot_code, reason, evidence_ref, status,
		                        reported_by, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		handoff.ID, lotCode, stageCode, req.Kind, req.Count,
		nullableString(req.ToLotCode), req.Reason, req.EvidenceRef, ClaimPending,
		req.ReportedBy, createdAt)
	if err != nil {
		return nil, err
	}
	eventID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := s.recomputeTx(ctx, tx, handoff.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.lossEventView(ctx, eventID)
}

// ConfirmLossEvent 确认或驳回一条凭证。仅确认后的凭证具备守恒效力；
// 确认重新分拣时在转出/转入两个批次上同时留下台账调整。
func (s *Service) ConfirmLossEvent(ctx context.Context, eventID int64, req ConfirmClaimRequest) (*LossEventView, error) {
	if strings.TrimSpace(req.ConfirmedBy) == "" {
		return nil, fmt.Errorf("%w: confirmed_by 必填", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	event, err := s.lossEventTx(ctx, tx, eventID)
	if err != nil {
		return nil, err
	}
	if event.Status != ClaimPending {
		return nil, fmt.Errorf("%w: 凭证已%s，不能重复处理", ErrConflict, claimStatusName(event.Status))
	}
	handoff, err := s.handoffByIDTx(ctx, tx, event.HandoffID)
	if err != nil {
		return nil, err
	}
	shortage := handoff.ExpectedCount - deref(handoff.ReceivedCount)

	if req.Approve {
		confirmedOthers, err := s.claimedCountTx(ctx, tx, handoff.ID, true)
		if err != nil {
			return nil, err
		}
		// 不含本条（仍为 PENDING）。
		if confirmedOthers+event.Count > shortage {
			return nil, fmt.Errorf("%w: 确认后凭证总数 %d 超过短数 %d",
				ErrClaimOvercount, confirmedOthers+event.Count, shortage)
		}
		if event.Kind == LossResort {
			target, err := s.getLotTx(ctx, tx, event.ToLotCode)
			if err != nil {
				return nil, err
			}
			if err := s.targetCanAcceptAtTx(ctx, tx, target, event.StageCode); err != nil {
				return nil, err
			}
		}
		confirmedAt := nowISO()
		if _, err := tx.ExecContext(ctx, `
			UPDATE loss_events SET status = ?, confirmed_by = ?, confirmed_at = ?
			WHERE id = ?`, ClaimConfirmed, req.ConfirmedBy, confirmedAt, eventID); err != nil {
			return nil, err
		}
		// 重新分拣在转出/转入两个批次上同时留下台账调整，跨批守恒可双向追溯；
		// 死亡不写调整账，其守恒效力直接来自 loss_events 的已确认数量。
		if event.Kind == LossResort {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO lot_adjustments(lot_code, delta, reason_kind, ref_loss_event_id,
				                            stage_code, created_at)
				VALUES (?,?,?,?,?,?)`,
				event.LotCode, -event.Count, "RESORT_OUT", event.ID, event.StageCode, nowISO()); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO lot_adjustments(lot_code, delta, reason_kind, ref_loss_event_id,
				                            stage_code, created_at)
				VALUES (?,?,?,?,?,?)`,
				event.ToLotCode, event.Count, "RESORT_IN", event.ID, event.StageCode, nowISO()); err != nil {
				return nil, err
			}
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE loss_events SET status = ?, confirmed_by = ?, confirmed_at = ?
			WHERE id = ?`, ClaimRejected, req.ConfirmedBy, nowISO(), eventID); err != nil {
			return nil, err
		}
	}

	if _, err := s.recomputeTx(ctx, tx, handoff.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.lossEventView(ctx, eventID)
}

// CompleteRelease 在放流环节配平后登记放流地点并完成放流。
func (s *Service) CompleteRelease(ctx context.Context, lotCode string, req CompleteReleaseRequest) (*HandoffView, error) {
	req.SiteCode = strings.TrimSpace(req.SiteCode)
	req.SiteName = strings.TrimSpace(req.SiteName)
	if req.SiteCode == "" || req.SiteName == "" {
		return nil, fmt.Errorf("%w: site_code 与 site_name 必填", ErrValidation)
	}
	if req.Longitude != nil && (*req.Longitude < -180 || *req.Longitude > 180) {
		return nil, fmt.Errorf("%w: 经度超出 [-180,180]", ErrValidation)
	}
	if req.Latitude != nil && (*req.Latitude < -90 || *req.Latitude > 90) {
		return nil, fmt.Errorf("%w: 纬度超出 [-90,90]", ErrValidation)
	}
	if (req.Longitude == nil) != (req.Latitude == nil) {
		return nil, fmt.Errorf("%w: 经纬度必须同时提供或同时省略", ErrValidation)
	}
	releasedAt, err := parseTimeOrDefault(req.ReleasedAt, nowISO())
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	handoff, err := s.handoffByStageTx(ctx, tx, lotCode, StageRelease)
	if err != nil {
		return nil, err
	}
	if handoff.Status != StatusBalanced {
		return nil, ErrReleaseRequiresBalance
	}
	if handoff.ReleasedAt != "" {
		return nil, fmt.Errorf("%w: 放流已完成", ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE handoffs SET release_site_code = ?, release_site_name = ?,
		                    longitude = ?, latitude = ?, released_at = ?
		WHERE id = ?`,
		req.SiteCode, req.SiteName, req.Longitude, req.Latitude, releasedAt, handoff.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE lots SET status = ? WHERE lot_code = ?", LotReleased, lotCode); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.handoffView(ctx, handoff.ID)
}

// RegisterFailure 登记设备故障。故障与数量账分离：它解释异常背景，但不能抵销短数。
// lotCode 为空时只登记设备侧事件。
func (s *Service) RegisterFailure(ctx context.Context, lotCode string, req FailureRequest) (*EquipmentFailureView, error) {
	if stageIndex(req.StageCode) < 0 {
		return nil, fmt.Errorf("%w: transfer_stage 非法", ErrValidation)
	}
	if strings.TrimSpace(req.EquipmentCode) == "" {
		return nil, fmt.Errorf("%w: equipment_code 必填", ErrValidation)
	}
	if strings.TrimSpace(req.OccurredAt) == "" {
		return nil, fmt.Errorf("%w: occurred_at 必填", ErrValidation)
	}
	occurredAt, err := parseTimeOrDefault(req.OccurredAt, "")
	if err != nil {
		return nil, err
	}
	if req.DowntimeMinutes != nil && *req.DowntimeMinutes < 0 {
		return nil, fmt.Errorf("%w: downtime_minutes 不能为负", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var lotRef any
	if strings.TrimSpace(lotCode) != "" {
		if _, err := s.getLotTx(ctx, tx, lotCode); err != nil {
			return nil, err
		}
		lotRef = lotCode
	}
	createdAt := nowISO()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO equipment_failures(lot_code, stage_code, equipment_code, description,
		                              downtime_minutes, occurred_at, reported_by, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		lotRef, req.StageCode, strings.TrimSpace(req.EquipmentCode), req.Description,
		req.DowntimeMinutes, occurredAt, req.ReportedBy, createdAt)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.failureView(ctx, id)
}

// RegisterMark 为增殖批次登记耳石或荧光标记；野生批次一律拒绝。
func (s *Service) RegisterMark(ctx context.Context, lotCode string, req MarkRequest) (*MarkView, error) {
	if req.MarkType != MarkOtolith && req.MarkType != MarkFluorescent {
		return nil, fmt.Errorf("%w: mark_type 必须为 OTOLITH 或 FLUORESCENT", ErrValidation)
	}
	req.MarkerCode = strings.TrimSpace(req.MarkerCode)
	if req.MarkerCode == "" {
		return nil, fmt.Errorf("%w: marker_code 必填", ErrValidation)
	}
	if req.MarkedCount <= 0 {
		return nil, fmt.Errorf("%w: marked_count 必须为正", ErrValidation)
	}
	markedAt, err := parseTimeOrDefault(req.MarkedAt, "")
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	lot, err := s.getLotTx(ctx, tx, lotCode)
	if err != nil {
		return nil, err
	}
	if lot.Origin != OriginHatchery {
		return nil, ErrWildMark
	}
	var existing int
	err = tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM marks WHERE mark_type = ? AND marker_code = ?",
		req.MarkType, req.MarkerCode).Scan(&existing)
	if err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, fmt.Errorf("%w: 标记 %s/%s 已登记", ErrAlreadyExists, req.MarkType, req.MarkerCode)
	}
	createdAt := nowISO()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO marks(lot_code, mark_type, marker_code, marked_count, marked_at,
		                  recorded_by, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		lotCode, req.MarkType, req.MarkerCode, req.MarkedCount, markedAt,
		req.RecordedBy, createdAt)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.markView(ctx, id)
}

// ListMarks 列出某批次的增殖标记。
func (s *Service) ListMarks(ctx context.Context, lotCode string) ([]MarkView, error) {
	if _, err := s.GetLot(ctx, lotCode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, lot_code, mark_type, marker_code, marked_count, marked_at,
		       recorded_by, created_at
		FROM marks WHERE lot_code = ? ORDER BY id`, lotCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMarks(rows)
}

// CreateSurvey 登记一次放流后的回捕调查。
func (s *Service) CreateSurvey(ctx context.Context, req SurveyRequest) (*SurveyView, error) {
	req.SurveyCode = strings.TrimSpace(req.SurveyCode)
	if req.SurveyCode == "" {
		return nil, fmt.Errorf("%w: survey_code 必填", ErrValidation)
	}
	if strings.TrimSpace(req.SiteName) == "" {
		return nil, fmt.Errorf("%w: site_name 必填", ErrValidation)
	}
	surveyDate, err := parseTimeOrDefault(req.SurveyDate, "")
	if err != nil {
		return nil, err
	}
	if req.Longitude != nil && (*req.Longitude < -180 || *req.Longitude > 180) {
		return nil, fmt.Errorf("%w: 经度超出 [-180,180]", ErrValidation)
	}
	if req.Latitude != nil && (*req.Latitude < -90 || *req.Latitude > 90) {
		return nil, fmt.Errorf("%w: 纬度超出 [-90,90]", ErrValidation)
	}
	if (req.Longitude == nil) != (req.Latitude == nil) {
		return nil, fmt.Errorf("%w: 经纬度必须同时提供或同时省略", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM surveys WHERE survey_code = ?", req.SurveyCode).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, fmt.Errorf("%w: 调查 %s 已存在", ErrAlreadyExists, req.SurveyCode)
	}
	createdAt := nowISO()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO surveys(survey_code, survey_date, site_code, site_name, longitude,
		                    latitude, method, investigators, note, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		req.SurveyCode, surveyDate, req.SiteCode, req.SiteName, req.Longitude,
		req.Latitude, req.Method, req.Investigators, req.Note, createdAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSurvey(ctx, req.SurveyCode)
}

// GetSurvey 查询单次调查。
func (s *Service) GetSurvey(ctx context.Context, surveyCode string) (*SurveyView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT survey_code, survey_date, site_code, site_name, longitude, latitude,
		       method, investigators, note, created_at
		FROM surveys WHERE survey_code = ?`, surveyCode)
	view := &SurveyView{}
	err := row.Scan(&view.SurveyCode, &view.SurveyDate, &view.SiteCode, &view.SiteName,
		&view.Longitude, &view.Latitude, &view.Method, &view.Investigators,
		&view.Note, &view.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: 调查 %s", ErrNotFound, surveyCode)
	}
	if err != nil {
		return nil, err
	}
	return view, nil
}

// AddRecapture 登记一次回捕检出并自动定性：
// 带标记且命中增殖标记 → CREDITED 并关联批次；无标记 → WILD；有标无录 → UNMATCHED_MARK。
func (s *Service) AddRecapture(ctx context.Context, surveyCode string, req RecaptureRequest) (*RecaptureView, error) {
	req.SpeciesCode = strings.TrimSpace(req.SpeciesCode)
	if req.SpeciesCode == "" {
		return nil, fmt.Errorf("%w: species_code 必填", ErrValidation)
	}
	if req.Count <= 0 {
		return nil, fmt.Errorf("%w: count 必须为正", ErrValidation)
	}
	req.MarkType = strings.TrimSpace(req.MarkType)
	req.MarkerCode = strings.TrimSpace(req.MarkerCode)
	if req.MarkType != "" && req.MarkType != MarkOtolith && req.MarkType != MarkFluorescent {
		return nil, fmt.Errorf("%w: mark_type 必须为 OTOLITH 或 FLUORESCENT", ErrValidation)
	}
	if req.MarkType == "" && req.MarkerCode != "" {
		return nil, fmt.Errorf("%w: 提供 marker_code 时必须同时提供 mark_type", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var surveyExists int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM surveys WHERE survey_code = ?", surveyCode).Scan(&surveyExists); err != nil {
		return nil, err
	}
	if surveyExists == 0 {
		return nil, fmt.Errorf("%w: 调查 %s", ErrNotFound, surveyCode)
	}

	resultKind := ResultWild
	var matchedLot sql.NullString
	var markType any
	var markerCode string
	if req.MarkType != "" {
		markType = req.MarkType
		markerCode = req.MarkerCode
		row := tx.QueryRowContext(ctx,
			"SELECT lot_code FROM marks WHERE mark_type = ? AND marker_code = ?",
			req.MarkType, req.MarkerCode)
		var lot string
		err := row.Scan(&lot)
		switch {
		case err == nil:
			resultKind = ResultCredited
			matchedLot = sql.NullString{String: lot, Valid: true}
		case errors.Is(err, sql.ErrNoRows):
			resultKind = ResultUnmatchedMark
		default:
			return nil, err
		}
	}

	createdAt := nowISO()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO recaptures(survey_code, species_code, count, mark_type, marker_code,
		                       origin_result, matched_lot_code, evidence_ref, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		surveyCode, req.SpeciesCode, req.Count, markType, markerCode,
		resultKind, matchedLot, req.EvidenceRef, createdAt)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.recaptureView(ctx, id)
}

// ListRecaptures 列出一次调查的全部检出。
func (s *Service) ListRecaptures(ctx context.Context, surveyCode string) ([]RecaptureView, error) {
	if _, err := s.GetSurvey(ctx, surveyCode); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, recaptureSelect+`
		FROM recaptures WHERE survey_code = ? ORDER BY id`, surveyCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var views []RecaptureView
	for rows.Next() {
		view, err := scanRecapture(rows)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, rows.Err()
}

// recomputeTx 依据当前接收数量与凭证状态重算交接的守恒结果。
// 配平条件：清点无短数，或短数被“已确认”的死亡/重分拣凭证恰好全部解释。
func (s *Service) recomputeTx(ctx context.Context, tx *sql.Tx, handoffID int64) (*HandoffView, error) {
	handoff, err := s.handoffByIDTx(ctx, tx, handoffID)
	if err != nil {
		return nil, err
	}
	if handoff.ReceivedCount == nil {
		if _, err := tx.ExecContext(ctx,
			"UPDATE handoffs SET status = ?, shortage_count = NULL WHERE id = ?",
			StatusDispatched, handoffID); err != nil {
			return nil, err
		}
		return s.handoffViewTx(ctx, tx, handoffID)
	}
	shortage := handoff.ExpectedCount - *handoff.ReceivedCount
	confirmed, err := s.claimedCountTx(ctx, tx, handoffID, true)
	if err != nil {
		return nil, err
	}
	status := StatusBlocked
	if shortage == 0 || confirmed == shortage {
		status = StatusBalanced
	}
	if confirmed > shortage {
		return nil, fmt.Errorf("%w: 已确认凭证 %d 超过短数 %d",
			ErrClaimOvercount, confirmed, shortage)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE handoffs SET status = ?, shortage_count = ? WHERE id = ?",
		status, shortage, handoffID); err != nil {
		return nil, err
	}
	return s.handoffViewTx(ctx, tx, handoffID)
}

// claimedCountTx 统计凭证覆盖数：confirmed=true 只计已确认，false 计待确认与已确认（不含驳回）。
func (s *Service) claimedCountTx(ctx context.Context, tx *sql.Tx, handoffID int64, confirmedOnly bool) (int, error) {
	query := "SELECT COALESCE(SUM(count),0) FROM loss_events WHERE handoff_id = ? AND status = ?"
	if !confirmedOnly {
		query = "SELECT COALESCE(SUM(count),0) FROM loss_events WHERE handoff_id = ? AND status IN (?,?)"
	}
	var total int
	var err error
	if confirmedOnly {
		err = tx.QueryRowContext(ctx, query, handoffID, ClaimConfirmed).Scan(&total)
	} else {
		err = tx.QueryRowContext(ctx, query, handoffID, ClaimPending, ClaimConfirmed).Scan(&total)
	}
	return total, err
}

// adjustedInTx 统计目标批次在指定环节已确认的重分拣并入量。
func (s *Service) adjustedInTx(ctx context.Context, tx *sql.Tx, lotCode, stageCode string) (int, error) {
	var total int
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(delta),0) FROM lot_adjustments
		WHERE lot_code = ? AND reason_kind = 'RESORT_IN' AND stage_code = ?`,
		lotCode, stageCode).Scan(&total)
	return total, err
}

// targetCanAcceptAtTx 校验重分拣目标批次尚未经过该环节，避免事后改写台账。
func (s *Service) targetCanAcceptAtTx(ctx context.Context, tx *sql.Tx, target *Lot, stageCode string) error {
	if stageIndex(stageCode) < stageIndex(target.StartStage) {
		return fmt.Errorf("%w: 目标批次从 %s 入链，不能接回更早的 %s 环节",
			ErrValidation, StageNames[target.StartStage], StageNames[stageCode])
	}
	if _, err := s.handoffByStageTx(ctx, tx, target.LotCode, stageCode); err == nil {
		return fmt.Errorf("%w: 目标批次 %s 已完成 %s 环节交接，鱼不能再并入",
			ErrConflict, target.LotCode, StageNames[stageCode])
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func parseTimeOrDefault(value string, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if fallback == "" {
			return "", fmt.Errorf("%w: 时间字段必填（ISO 8601 带偏移，或 YYYY-MM-DD）", ErrValidation)
		}
		return fallback, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("%w: 时间 %q 不是 ISO 8601 格式", ErrValidation, value)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func claimStatusName(status string) string {
	switch status {
	case ClaimConfirmed:
		return "确认"
	case ClaimRejected:
		return "驳回"
	default:
		return "提交"
	}
}
