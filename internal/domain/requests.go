package domain

// CreateLotRequest 登记一批鱼。origin 必填 WILD/HATCHERY，从分拣入链时 start_stage 留空。
type CreateLotRequest struct {
	LotCode      string `json:"lot_code"`
	SpeciesCode  string `json:"species_code"`
	Origin       string `json:"origin"`
	Source       string `json:"source"`
	InitialCount int    `json:"initial_count"`
	StartStage   string `json:"start_stage,omitempty"` // 仅重新分拣并出的新批次使用
	Note         string `json:"note,omitempty"`
	RecordedBy   string `json:"recorded_by,omitempty"`
}

// DispatchRequest 登记某环节发出（运输箱清单逐箱登记）。
type DispatchRequest struct {
	StageCode  string          `json:"transfer_stage"` // 对应交换字段 transfer_stage
	Containers []ContainerItem `json:"containers"`
	Note       string          `json:"note,omitempty"`
	ReportedBy string          `json:"reported_by,omitempty"`
}

// ReceiveRequest 为接收方逐箱清点；未列出的箱子按 0 尾处理，强制逐箱登记。
type ReceiveRequest struct {
	Containers []ContainerItem `json:"containers"`
	ReceiverBy string          `json:"receiver_by,omitempty"`
	ReceivedAt string          `json:"received_at,omitempty"`
}

// LossRequest 报告死亡或重新分拣凭证。
type LossRequest struct {
	Kind        string `json:"kind"` // DEATH / RESORT
	Count       int    `json:"count"`
	ToLotCode   string `json:"to_lot_code,omitempty"` // RESORT 必填：并入的目标批次
	Reason      string `json:"reason,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	ReportedBy  string `json:"reported_by,omitempty"`
}

// ConfirmClaimRequest 确认/驳回凭证。
type ConfirmClaimRequest struct {
	Approve     bool   `json:"approve"`
	ConfirmedBy string `json:"confirmed_by"`
	Note        string `json:"note,omitempty"`
}

// CompleteReleaseRequest 放流地点登记（配平后完成放流）。
type CompleteReleaseRequest struct {
	SiteCode   string   `json:"site_code"`
	SiteName   string   `json:"site_name"`
	Longitude  *float64 `json:"longitude,omitempty"`
	Latitude   *float64 `json:"latitude,omitempty"`
	ReleasedAt string   `json:"released_at,omitempty"`
}

// FailureRequest 设备故障登记。
type FailureRequest struct {
	StageCode       string `json:"transfer_stage"`
	EquipmentCode   string `json:"equipment_code"`
	Description     string `json:"description,omitempty"`
	DowntimeMinutes *int   `json:"downtime_minutes,omitempty"`
	OccurredAt      string `json:"occurred_at"`
	ReportedBy      string `json:"reported_by,omitempty"`
}

// MarkRequest 增殖鱼苗标记登记。
type MarkRequest struct {
	MarkType    string `json:"mark_type"`   // OTOLITH / FLUORESCENT
	MarkerCode  string `json:"marker_code"` // 标记批号/色号/纹理解码字
	MarkedCount int    `json:"marked_count"`
	MarkedAt    string `json:"marked_at"`
	RecordedBy  string `json:"recorded_by,omitempty"`
}

// SurveyRequest 回捕调查登记。
type SurveyRequest struct {
	SurveyCode    string   `json:"survey_code"`
	SurveyDate    string   `json:"survey_date"`
	SiteCode      string   `json:"site_code,omitempty"`
	SiteName      string   `json:"site_name"`
	Longitude     *float64 `json:"longitude,omitempty"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Method        string   `json:"method,omitempty"`
	Investigators string   `json:"investigators,omitempty"`
	Note          string   `json:"note,omitempty"`
}

// RecaptureRequest 登记一次回捕检出。
// 无标记时 mark_type/marker_code 留空，系统定性 WILD 且不计成效；
// 有标记时系统按标记追溯增殖批次，查无此标记记 UNMATCHED_MARK。
type RecaptureRequest struct {
	SpeciesCode string `json:"species_code"`
	Count       int    `json:"count"`
	MarkType    string `json:"mark_type,omitempty"`
	MarkerCode  string `json:"marker_code,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}
