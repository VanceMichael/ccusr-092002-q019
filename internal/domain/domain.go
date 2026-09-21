// Package domain 定义过坝链路核验的枚举与逐段守恒规则。
package domain

// StageCode 是过坝链路上的环节代码。
type StageCode string

const (
	StageCollection      StageCode = "COLLECTION"       // 集鱼槽收集
	StageSorting         StageCode = "SORTING"          // 站点分拣
	StageRailLift        StageCode = "RAIL_LIFT"        // 155 米轨道提升
	StageLandTransport   StageCode = "LAND_TRANSPORT"   // 无人车陆运
	StageVesselTransport StageCode = "VESSEL_TRANSPORT" // 运鱼船船运
	StageReleaseSite     StageCode = "RELEASE_SITE"     // 放流点
)

// OrderedStages 是鱼从下游侧到放流点的标准环节顺序。
var OrderedStages = []StageCode{
	StageCollection,
	StageSorting,
	StageRailLift,
	StageLandTransport,
	StageVesselTransport,
	StageReleaseSite,
}

// StageNames 供查询接口输出中文环节名。
var StageNames = map[StageCode]string{
	StageCollection:      "集鱼槽收集",
	StageSorting:         "站点分拣",
	StageRailLift:        "轨道提升(155米)",
	StageLandTransport:   "无人车陆运",
	StageVesselTransport: "运鱼船船运",
	StageReleaseSite:     "放流点",
}

// ValidStage 判断环节代码是否合法。
func ValidStage(s string) bool {
	_, ok := StageNames[StageCode(s)]
	return ok
}

// StageIndex 返回环节在标准链路中的位置，非法环节返回 -1。
func StageIndex(s StageCode) int {
	for i, st := range OrderedStages {
		if st == s {
			return i
		}
	}
	return -1
}

// IsAdjacentTransfer 判断一段交接是否沿标准链路相邻（含同段，同段只能由重分拣产生）。
// from/to 必须都是合法环节，且 to 位于 from 之后且最多跨过一个环节。
func IsAdjacentTransfer(from, to StageCode) bool {
	fi, ti := StageIndex(from), StageIndex(to)
	return fi >= 0 && ti >= 0 && ti > fi && ti-fi == 1
}

// 来源类型。
const (
	OriginWild     = "WILD"     // 天然过坝野生鱼，不计入增殖放流成效
	OriginHatchery = "HATCHERY" // 增殖站鱼苗，其标记回捕才计入放流成效
)

// 记录与交接状态。
const (
	StatusPending   = "PENDING"
	StatusConfirmed = "CONFIRMED"
	StatusRejected  = "REJECTED"

	HandoverSent      = "SENT"
	HandoverConfirmed = "CONFIRMED"
	HandoverDisputed  = "DISPUTED"

	FaultOpen     = "OPEN"
	FaultResolved = "RESOLVED"
)
