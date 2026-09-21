package store

// Lot 是鱼类批次主记录。
type Lot struct {
	LotRef       string `json:"lot_ref"`
	SpeciesCode  string `json:"species_code"`
	Origin       string `json:"origin"`
	InitialCount int    `json:"initial_count"`
	EntryStage   string `json:"entry_stage"`
	CurrentStage string `json:"current_stage"`
	Status       string `json:"status"`
	Notes        string `json:"notes,omitempty"`
	CreatedAt    string `json:"created_at"`
}

type CreateLotInput struct {
	LotRef      string `json:"lot_ref"`
	SpeciesCode string `json:"species_code"`
	Origin      string `json:"origin"` // WILD 或 HATCHERY，默认 WILD
	Count       int    `json:"count"`
	EntryStage  string `json:"entry_stage"`
	Notes       string `json:"notes"`
	CreatedBy   string `json:"created_by"`
	OccurredAt  string `json:"occurred_at"`
}

// ContainerCount 是一次交接中单个运输箱的清点数量。
type ContainerCount struct {
	ContainerRef  string `json:"container_ref"`
	SentCount     int    `json:"sent_count"`
	ReceivedCount int    `json:"received_count,omitempty"`
}

type DispatchInput struct {
	LotRef     string           `json:"lot_ref"`
	FromStage  string           `json:"from_stage"`
	ToStage    string           `json:"to_stage"`
	SentCount  int              `json:"sent_count"`
	Containers []ContainerCount `json:"containers"`
	SentAt     string           `json:"sent_at"`
	SentBy     string           `json:"sent_by"`
	Note       string           `json:"note"`
}

type ReceiveInput struct {
	HandoverID    string           `json:"handover_id"`
	Received      []ContainerCount `json:"containers"` // 按箱复核；为空且 received_count>0 时视为整单清点
	ReceivedCount int              `json:"received_count"`
	ReceivedAt    string           `json:"received_at"`
	ReceivedBy    string           `json:"received_by"`
	Note          string           `json:"note"`
}

type Handover struct {
	HandoverID    string           `json:"handover_id"`
	LotRef        string           `json:"lot_ref"`
	Seq           int              `json:"seq"`
	FromStage     string           `json:"from_stage"`
	ToStage       string           `json:"to_stage"`
	SentCount     int              `json:"sent_count"`
	ReceivedCount *int             `json:"received_count,omitempty"`
	Status        string           `json:"status"`
	Discrepancy   *int             `json:"discrepancy,omitempty"`
	SentAt        string           `json:"sent_at"`
	ReceivedAt    string           `json:"received_at,omitempty"`
	SentBy        string           `json:"sent_by,omitempty"`
	ReceivedBy    string           `json:"received_by,omitempty"`
	Note          string           `json:"note,omitempty"`
	Containers    []ContainerCount `json:"containers"`
}

type LossInput struct {
	LotRef     string `json:"lot_ref"`
	HandoverID string `json:"handover_id"`
	StageCode  string `json:"stage_code"`
	Count      int    `json:"count"`
	Cause      string `json:"cause"`
	FaultID    string `json:"fault_id"`
	OccurredAt string `json:"occurred_at"`
	RecordedBy string `json:"recorded_by"`
	Notes      string `json:"notes"`
}

type LossEvent struct {
	LossID      string `json:"loss_id"`
	LotRef      string `json:"lot_ref"`
	HandoverID  string `json:"handover_id,omitempty"`
	StageCode   string `json:"stage_code"`
	Count       int    `json:"count"`
	Cause       string `json:"cause"`
	Status      string `json:"status"`
	FaultID     string `json:"fault_id,omitempty"`
	OccurredAt  string `json:"occurred_at"`
	ConfirmedAt string `json:"confirmed_at,omitempty"`
	RecordedBy  string `json:"recorded_by,omitempty"`
	ConfirmedBy string `json:"confirmed_by,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

type ResortInput struct {
	FromLotRef string `json:"from_lot_ref"`
	ToLotRef   string `json:"to_lot_ref"` // 可空，由服务新建零初始数量批次
	HandoverID string `json:"handover_id"`
	ToSpecies  string `json:"to_species_code"`
	ToOrigin   string `json:"to_origin"`
	StageCode  string `json:"stage_code"`
	Count      int    `json:"count"`
	Reason     string `json:"reason"`
	OccurredAt string `json:"occurred_at"`
	RecordedBy string `json:"recorded_by"`
}

type ResortEvent struct {
	ResortID    string `json:"resort_id"`
	FromLotRef  string `json:"from_lot_ref"`
	ToLotRef    string `json:"to_lot_ref"`
	HandoverID  string `json:"handover_id,omitempty"`
	StageCode   string `json:"stage_code"`
	Count       int    `json:"count"`
	Reason      string `json:"reason,omitempty"`
	Status      string `json:"status"`
	OccurredAt  string `json:"occurred_at"`
	ConfirmedAt string `json:"confirmed_at,omitempty"`
	RecordedBy  string `json:"recorded_by,omitempty"`
	ConfirmedBy string `json:"confirmed_by,omitempty"`
}

type FaultInput struct {
	FaultID      string   `json:"fault_id"` // 可空，自动生成
	EquipmentRef string   `json:"equipment_ref"`
	StageCode    string   `json:"stage_code"`
	FaultType    string   `json:"fault_type"`
	Description  string   `json:"description"`
	LotRefs      []string `json:"lot_refs"`
	StartedAt    string   `json:"started_at"`
	RecordedBy   string   `json:"recorded_by"`
	Notes        string   `json:"notes"`
}

type EquipmentFault struct {
	FaultID      string   `json:"fault_id"`
	EquipmentRef string   `json:"equipment_ref"`
	StageCode    string   `json:"stage_code"`
	FaultType    string   `json:"fault_type"`
	Description  string   `json:"description,omitempty"`
	Status       string   `json:"status"`
	StartedAt    string   `json:"started_at"`
	ResolvedAt   string   `json:"resolved_at,omitempty"`
	RecordedBy   string   `json:"recorded_by,omitempty"`
	Notes        string   `json:"notes,omitempty"`
	LotRefs      []string `json:"lot_refs"`
}

type ReleaseInput struct {
	LotRef     string   `json:"lot_ref"`
	ReleaseRef string   `json:"release_ref"`
	Count      int      `json:"count"`
	Waterbody  string   `json:"waterbody"`
	SiteName   string   `json:"site_name"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
	ReleasedAt string   `json:"released_at"`
	ReleasedBy string   `json:"released_by"`
	Notes      string   `json:"notes"`
}

type Release struct {
	ReleaseID  string   `json:"release_id"`
	LotRef     string   `json:"lot_ref"`
	ReleaseRef string   `json:"release_ref,omitempty"`
	Count      int      `json:"count"`
	Waterbody  string   `json:"waterbody"`
	SiteName   string   `json:"site_name,omitempty"`
	Latitude   *float64 `json:"latitude,omitempty"`
	Longitude  *float64 `json:"longitude,omitempty"`
	ReleasedAt string   `json:"released_at"`
	ReleasedBy string   `json:"released_by,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

type MarkInput struct {
	LotRef      string `json:"lot_ref"`
	MarkType    string `json:"mark_type"` // OTOLITH / FLUORESCENT / PIT / OTHER
	MarkCode    string `json:"mark_code"`
	MarkedCount int    `json:"marked_count"`
	MarkedAt    string `json:"marked_at"`
	Method      string `json:"method"`
	Notes       string `json:"notes"`
}

type Mark struct {
	MarkID      string `json:"mark_id"`
	LotRef      string `json:"lot_ref"`
	MarkType    string `json:"mark_type"`
	MarkCode    string `json:"mark_code"`
	MarkedCount int    `json:"marked_count"`
	MarkedAt    string `json:"marked_at"`
	Method      string `json:"method,omitempty"`
	Notes       string `json:"notes,omitempty"`
}

type SurveyInput struct {
	SurveyRef       string   `json:"survey_ref"`
	SurveyDate      string   `json:"survey_date"`
	Waterbody       string   `json:"waterbody"`
	SiteName        string   `json:"site_name"`
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	Method          string   `json:"method"`
	InvestigatorRef string   `json:"investigator_ref"`
	Notes           string   `json:"notes"`
}

type Survey struct {
	SurveyID        string   `json:"survey_id"`
	SurveyRef       string   `json:"survey_ref,omitempty"`
	SurveyDate      string   `json:"survey_date"`
	Waterbody       string   `json:"waterbody"`
	SiteName        string   `json:"site_name,omitempty"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
	Method          string   `json:"method,omitempty"`
	InvestigatorRef string   `json:"investigator_ref,omitempty"`
	Notes           string   `json:"notes,omitempty"`
}

type RecaptureInput struct {
	MarkCode    string `json:"mark_code"` // 空表示无标记个体（不会匹配放流批次）
	SpeciesCode string `json:"species_code"`
	Count       int    `json:"count"`
	Notes       string `json:"notes"`
}

type Recapture struct {
	RecaptureID   string `json:"recapture_id"`
	SurveyID      string `json:"survey_id"`
	MarkCode      string `json:"mark_code,omitempty"`
	SpeciesCode   string `json:"species_code"`
	Count         int    `json:"count"`
	MatchStatus   string `json:"match_status"`
	MatchedLotRef string `json:"matched_lot_ref,omitempty"`
	Notes         string `json:"notes,omitempty"`
}
