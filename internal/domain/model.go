// Package domain 实现鱼类过坝批次核验的业务规则：逐段交接、数量守恒、
// 死亡/重分拣凭证确认、设备故障登记，以及增殖标记与回捕成效的关联。
package domain

import "errors"

// 鱼类来源：天然野生过坝鱼与增殖放流鱼苗必须分账。
const (
	OriginWild     = "WILD"     // 天然鱼：只核验过坝，永不计入放流成效
	OriginHatchery = "HATCHERY" // 增殖鱼苗：带耳石/荧光标记，回捕命中后才计入成效
)

// 五个固定环节及其流转顺序。
const (
	StageSorting         = "SORTING"          // 分拣
	StageHoist155m       = "HOIST_155M"       // 155 米轨道提升
	StageLandTransport   = "LAND_TRANSPORT"   // 陆运（无人车）
	StageVesselTransport = "VESSEL_TRANSPORT" // 船运（运鱼船）
	StageRelease         = "RELEASE"          // 放流
)

// StageOrder 为环节顺序表，序号从 1 开始。
var StageOrder = []string{
	StageSorting,
	StageHoist155m,
	StageLandTransport,
	StageVesselTransport,
	StageRelease,
}

// StageNames 供查询输出中文环节名。
var StageNames = map[string]string{
	StageSorting:         "分拣",
	StageHoist155m:       "155米轨道提升",
	StageLandTransport:   "陆运",
	StageVesselTransport: "船运",
	StageRelease:         "放流",
}

func stageIndex(code string) int {
	for i, stage := range StageOrder {
		if stage == code {
			return i
		}
	}
	return -1
}

// handoff 状态
const (
	StatusDispatched = "DISPATCHED" // 已发出，等待接收清点
	StatusBlocked    = "BLOCKED"    // 清点短数且凭证不足或未确认，禁止继续流转
	StatusBalanced   = "BALANCED"   // 守恒配平（数量相符，或短数全部由已确认凭证解释）
)

// 损耗凭证类型与状态
const (
	LossDeath  = "DEATH"  // 死亡
	LossResort = "RESORT" // 重新分拣（鱼被并入另一批次）

	ClaimPending   = "PENDING"
	ClaimConfirmed = "CONFIRMED"
	ClaimRejected  = "REJECTED"
)

// 标记类型
const (
	MarkOtolith     = "OTOLITH"     // 耳石（热）标记
	MarkFluorescent = "FLUORESCENT" // 荧光标记
)

// 回捕定性
const (
	ResultCredited      = "CREDITED"       // 命中增殖标记 → 计入放流成效
	ResultWild          = "WILD"           // 无标记天然鱼 → 不计成效
	ResultUnmatchedMark = "UNMATCHED_MARK" // 有标记但系统查无此标记批次 → 不计成效，待人工核查
)

// 批次状态
const (
	LotActive   = "ACTIVE"
	LotReleased = "RELEASED"
	LotClosed   = "CLOSED"
)

// 业务错误。HTTP 层据此映射状态码。
var (
	ErrNotFound               = errors.New("记录不存在")
	ErrConflict               = errors.New("当前状态不允许该操作")
	ErrValidation             = errors.New("请求参数不合法")
	ErrAlreadyExists          = errors.New("记录已存在")
	ErrStageOutOfOrder        = errors.New("必须按分拣→提升→陆运→船运→放流的顺序逐段交接")
	ErrPriorUnbalanced        = errors.New("上一环节尚未配平，禁止继续流转")
	ErrClaimInsufficient      = errors.New("短数缺少经确认的死亡或重新分拣记录，无法配平")
	ErrClaimOvercount         = errors.New("凭证数量超过本环节短数")
	ErrWildMark               = errors.New("天然野生鱼批次不得登记增殖标记")
	ErrMarkNotFound           = errors.New("标记在系统中无对应增殖批次")
	ErrReleaseRequiresBalance = errors.New("放流清点配平后必须登记放流地点才能完成放流")
)
