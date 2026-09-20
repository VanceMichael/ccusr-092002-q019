package domain

import "time"

// Lot 为鱼批档案。
type Lot struct {
	LotCode      string `json:"lot_code"`
	SpeciesCode  string `json:"species_code"`
	Origin       string `json:"origin"`
	Source       string `json:"source"`
	InitialCount int    `json:"initial_count"`
	StartStage   string `json:"start_stage"`
	Status       string `json:"status"`
	Note         string `json:"note"`
	RecordedBy   string `json:"recorded_by"`
	CreatedAt    string `json:"created_at"`
}

// ContainerItem 是一次交接中的单个运输箱清单。
type ContainerItem struct {
	ContainerRef  string `json:"container_ref"`
	ExpectedCount int    `json:"expected_count"`
	ReceivedCount *int   `json:"received_count,omitempty"`
}

// HandoffView 是一次逐段交接及其守恒结果。
type HandoffView struct {
	ID              int64           `json:"id"`
	LotCode         string          `json:"lot_code"`
	StageCode       string          `json:"stage_code"`
	StageName       string          `json:"stage_name"`
	Seq             int             `json:"seq"`
	ExpectedCount   int             `json:"expected_count"`
	ReceivedCount   *int            `json:"received_count,omitempty"`
	ShortageCount   *int            `json:"shortage_count,omitempty"`
	Status          string          `json:"status"`
	ReportedBy      string          `json:"reported_by,omitempty"`
	ReceiverBy      string          `json:"receiver_by,omitempty"`
	Note            string          `json:"note,omitempty"`
	CreatedAt       string          `json:"created_at"`
	ReceivedAt      string          `json:"received_at,omitempty"`
	Containers      []ContainerItem `json:"containers,omitempty"`
	ConfirmedLoss   int             `json:"confirmed_loss"` // 已确认凭证覆盖数
	PendingLoss     int             `json:"pending_loss"`   // 待确认凭证覆盖数
	Balanced        bool            `json:"balanced"`
	ReleaseSiteCode string          `json:"release_site_code,omitempty"`
	ReleaseSiteName string          `json:"release_site_name,omitempty"`
	Longitude       *float64        `json:"longitude,omitempty"`
	Latitude        *float64        `json:"latitude,omitempty"`
	ReleasedAt      string          `json:"released_at,omitempty"`
}

// LossEventView 为短数凭证。
type LossEventView struct {
	ID          int64  `json:"id"`
	HandoffID   int64  `json:"handoff_id"`
	LotCode     string `json:"lot_code"`
	StageCode   string `json:"stage_code"`
	Kind        string `json:"kind"`
	Count       int    `json:"count"`
	ToLotCode   string `json:"to_lot_code,omitempty"`
	Reason      string `json:"reason,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
	Status      string `json:"status"`
	ReportedBy  string `json:"reported_by,omitempty"`
	ConfirmedBy string `json:"confirmed_by,omitempty"`
	CreatedAt   string `json:"created_at"`
	ConfirmedAt string `json:"confirmed_at,omitempty"`
}

// EquipmentFailureView 为设备故障记录。
type EquipmentFailureView struct {
	ID              int64  `json:"id"`
	LotCode         string `json:"lot_code,omitempty"`
	StageCode       string `json:"stage_code"`
	StageName       string `json:"stage_name"`
	EquipmentCode   string `json:"equipment_code"`
	Description     string `json:"description,omitempty"`
	DowntimeMinutes *int   `json:"downtime_minutes,omitempty"`
	OccurredAt      string `json:"occurred_at"`
	ReportedBy      string `json:"reported_by,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// MarkView 为增殖标记。
type MarkView struct {
	ID          int64  `json:"id"`
	LotCode     string `json:"lot_code"`
	MarkType    string `json:"mark_type"`
	MarkerCode  string `json:"marker_code"`
	MarkedCount int    `json:"marked_count"`
	MarkedAt    string `json:"marked_at"`
	RecordedBy  string `json:"recorded_by,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// SurveyView 为回捕调查航次/批次。
type SurveyView struct {
	SurveyCode    string   `json:"survey_code"`
	SurveyDate    string   `json:"survey_date"`
	SiteCode      string   `json:"site_code,omitempty"`
	SiteName      string   `json:"site_name"`
	Longitude     *float64 `json:"longitude,omitempty"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Method        string   `json:"method,omitempty"`
	Investigators string   `json:"investigators,omitempty"`
	Note          string   `json:"note,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

// RecaptureView 为单条回捕检出及其成效定性。
type RecaptureView struct {
	ID             int64  `json:"id"`
	SurveyCode     string `json:"survey_code"`
	SpeciesCode    string `json:"species_code"`
	Count          int    `json:"count"`
	MarkType       string `json:"mark_type,omitempty"`
	MarkerCode     string `json:"marker_code,omitempty"`
	OriginResult   string `json:"origin_result"`
	MatchedLotCode string `json:"matched_lot_code,omitempty"`
	EvidenceRef    string `json:"evidence_ref,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// LotTrace 是管理部门查询一批鱼时看到的全链路视图。
type LotTrace struct {
	Lot               Lot                    `json:"lot"`
	Handoffs          []HandoffView          `json:"handoffs"`
	LossEvents        []LossEventView        `json:"loss_events"`
	Adjustments       []AdjustmentView       `json:"adjustments"`
	EquipmentFailures []EquipmentFailureView `json:"equipment_failures"`
	Marks             []MarkView             `json:"marks"`
	Conservation      ConservationSummary    `json:"conservation"`
	Release           *ReleaseSummary        `json:"release,omitempty"`
	Monitoring        MonitoringSummary      `json:"monitoring"`
}

// AdjustmentView 为重分拣在批次账上留下的调整痕迹。
type AdjustmentView struct {
	ID             int64  `json:"id"`
	LotCode        string `json:"lot_code"`
	Delta          int    `json:"delta"`
	ReasonKind     string `json:"reason_kind"`
	RefLossEventID int64  `json:"ref_loss_event_id"`
	StageCode      string `json:"stage_code"`
	CreatedAt      string `json:"created_at"`
}

// ConservationSummary 汇总一批鱼的守恒账：期初 ± 重分拣 − 死亡 = 放流/在链。
type ConservationSummary struct {
	InitialCount       int  `json:"initial_count"`
	ResortIn           int  `json:"resort_in"`
	ResortOut          int  `json:"resort_out"`
	ConfirmedDeaths    int  `json:"confirmed_deaths"`
	PendingDeathClaims int  `json:"pending_death_claims"`
	AccountedCount     int  `json:"accounted_count"` // 期初 + 并入 − 转出 − 已确认死亡
	ReleasedCount      int  `json:"released_count"`  // 放流环节接收并配平的数量
	Balanced           bool `json:"balanced"`        // 全链路无 BLOCKED、无待确认凭证
}

// ReleaseSummary 描述放流落点。
type ReleaseSummary struct {
	SiteCode   string   `json:"site_code"`
	SiteName   string   `json:"site_name"`
	Count      int      `json:"count"`
	Longitude  *float64 `json:"longitude,omitempty"`
	Latitude   *float64 `json:"latitude,omitempty"`
	ReleasedAt string   `json:"released_at"`
}

// MonitoringSummary 关联数月后的监测证据。
type MonitoringSummary struct {
	CreditedRecaptures int           `json:"credited_recaptures"` // 命中本批增殖标记的回捕尾数（成效）
	WildRecaptures     int           `json:"wild_recaptures"`     // 无标记野生鱼（不混入成效）
	UnmatchedMarks     int           `json:"unmatched_marks"`     // 有标无录，待核查
	Surveys            []SurveyRecap `json:"surveys"`
}

// SurveyRecap 是监测汇总中按调查列出的简表。
type SurveyRecap struct {
	SurveyCode string `json:"survey_code"`
	SurveyDate string `json:"survey_date"`
	SiteName   string `json:"site_name"`
	Credited   int    `json:"credited"`
	Wild       int    `json:"wild"`
	Unmatched  int    `json:"unmatched"`
}

// nowISO 返回带时区偏移的当前时间字符串。
func nowISO() string {
	return time.Now().Format(time.RFC3339)
}
