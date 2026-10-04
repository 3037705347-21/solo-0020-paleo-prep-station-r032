package domain

import (
	"strings"

	"example.com/paleo-prep-station/internal/contracts"
)

// Location 是标本当前所在的库位。除 released 外均为馆内库位；
// released 表示标本已办理研究放行并离开馆藏，不再位于任何库内位置。
type Location string

const (
	LocationUndergroundStorage Location = "underground_storage"
	LocationRepairBench        Location = "repair_bench"
	LocationColdStorage        Location = "cold_storage"
	LocationTemporaryDisplay   Location = "temporary_display"
	LocationReleased           Location = "released"
)

// ParseStorageLocation 校验客户端提交的目标库位。released 只能由
// 研究放行流程写入，不能作为普通移动的去向。
func ParseStorageLocation(value string) (Location, error) {
	cleaned := strings.TrimSpace(value)
	switch Location(cleaned) {
	case LocationUndergroundStorage, LocationRepairBench, LocationColdStorage, LocationTemporaryDisplay:
		return Location(cleaned), nil
	case LocationReleased:
		return "", contracts.Invalid("invalid_location", "放行出库只能由研究放行流程完成，不能作为移动去向")
	default:
		return "", contracts.Invalid(
			"invalid_location",
			"库位必须是 underground_storage、repair_bench、cold_storage、temporary_display 之一",
		)
	}
}
