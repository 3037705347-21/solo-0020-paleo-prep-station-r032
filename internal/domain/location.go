package domain

import (
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// LocationCode 是标本在设施内的库位标识。前四个为受管实体库位，
// released 仅由放行流程写入，表示标本已经离库、不再属于任何在管库位。
type LocationCode string

const (
	LocationUndergroundStorage LocationCode = "underground_storage" // 地下库
	LocationPrepBench          LocationCode = "prep_bench"          // 修复台
	LocationColdCabinet        LocationCode = "cold_cabinet"        // 冷柜
	LocationTemporaryDisplay   LocationCode = "temporary_display"   // 临时展柜
	LocationReleased           LocationCode = "released"            // 放行离库
)

// InitialLocation 是标本完成登记后的默认库位。
const InitialLocation = LocationUndergroundStorage

var storageLocationCodes = map[LocationCode]struct{}{
	LocationUndergroundStorage: {},
	LocationPrepBench:          {},
	LocationColdCabinet:        {},
	LocationTemporaryDisplay:   {},
}

// MoveRecord 是一次库位交接的不可变记录。
type MoveRecord struct {
	ID           string       `json:"id"`
	SpecimenID   string       `json:"specimen_id"`
	Sequence     int          `json:"sequence"`
	FromLocation LocationCode `json:"from_location"`
	ToLocation   LocationCode `json:"to_location"`
	HandledBy    string       `json:"handled_by"`
	Reason       string       `json:"reason"`
	CreatedAt    time.Time    `json:"created_at"`
}

// LocationLedger 是每份标本唯一的库位事实：一个当前库位加一条只追加的交接轨迹。
// 当前库位与移动历史必须在同一台账内一起更新，避免只写一半。
type LocationLedger struct {
	SpecimenID      string       `json:"specimen_id"`
	CurrentLocation LocationCode `json:"current_location"`
	Moves           []MoveRecord `json:"moves"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// NewLocationLedger 在登记标本的同一事务中创建，保证标本自存在起就有且仅有一个当前库位。
func NewLocationLedger(specimenID string, now time.Time) (LocationLedger, error) {
	if specimenID == "" {
		return LocationLedger{}, contracts.Internal("missing_generated_id", "库位台账缺少标本标识", nil)
	}
	stamp := now.UTC()
	return LocationLedger{
		SpecimenID:      specimenID,
		CurrentLocation: InitialLocation,
		Moves:           make([]MoveRecord, 0),
		UpdatedAt:       stamp,
	}, nil
}

// IsOnSite 表示标本当前是否仍在四个受管库位之一。放行离库后返回 false。
func (l LocationLedger) IsOnSite() bool {
	_, ok := storageLocationCodes[l.CurrentLocation]
	return ok
}

// RecordMove 在台账上登记一次交接。
//
// 返回值 replayed 为 true 时表示这是对上一条交接的完全重复提交，
// 直接回放已有记录、不追加新轨迹；任何拒绝都在修改台账之前返回错误，
// 因此调用方要么拿到一条完整记录，要么什么都不留下。
func (l *LocationLedger) RecordMove(
	id string,
	fromLocation string,
	toLocation string,
	handledBy string,
	reason string,
	now time.Time,
) (record MoveRecord, replayed bool, err error) {
	if id == "" {
		return MoveRecord{}, false, contracts.Internal("missing_generated_id", "移动记录标识未生成", nil)
	}
	source, err := parseStorageLocation(fromLocation, "来源库位")
	if err != nil {
		return MoveRecord{}, false, err
	}
	target, err := parseStorageLocation(toLocation, "目标库位")
	if err != nil {
		return MoveRecord{}, false, err
	}
	handler, err := requiredText("经手人", handledBy, 80)
	if err != nil {
		return MoveRecord{}, false, err
	}
	why, err := requiredText("交接原因", reason, 240)
	if err != nil {
		return MoveRecord{}, false, err
	}
	if l.CurrentLocation == LocationReleased {
		return MoveRecord{}, false, contracts.Conflict(
			"specimen_already_released", "标本已放行离库，不能再登记库位移动")
	}
	// 幂等重放先于来源一致性判断：重试是在移动生效之后到达的，
	// 请求中的来源库位必然停留在移动前，不能因此拒绝完全相同的重复提交。
	// 只有"与上一条交接逐字段一致"才算重放；离开后再回同一库位不会命中。
	if len(l.Moves) > 0 {
		last := l.Moves[len(l.Moves)-1]
		if last.FromLocation == source &&
			last.ToLocation == target &&
			last.HandledBy == handler &&
			last.Reason == why {
			return last, true, nil
		}
	}
	if source != l.CurrentLocation {
		return MoveRecord{}, false, contracts.Conflict(
			"movement_source_mismatch", "来源库位与当前库位不一致，交接已拒绝")
	}
	if source == target {
		return MoveRecord{}, false, contracts.Conflict(
			"specimen_already_at_location", "标本已在目标库位，不能登记原地移动")
	}
	stamp := now.UTC()
	record = MoveRecord{
		ID:           id,
		SpecimenID:   l.SpecimenID,
		Sequence:     len(l.Moves) + 1,
		FromLocation: source,
		ToLocation:   target,
		HandledBy:    handler,
		Reason:       why,
		CreatedAt:    stamp,
	}
	l.Moves = append(l.Moves, record)
	l.CurrentLocation = target
	l.UpdatedAt = stamp
	return record, false, nil
}

// CheckOut 在放行成功的同一事务中把台账结转到离库状态，
// 使放行后的标本不可能再被显示在任何实体库位内。
func (l *LocationLedger) CheckOut(
	id string,
	handledBy string,
	reason string,
	now time.Time,
) (MoveRecord, error) {
	if id == "" {
		return MoveRecord{}, contracts.Internal("missing_generated_id", "移动记录标识未生成", nil)
	}
	if l.CurrentLocation == LocationReleased {
		return MoveRecord{}, contracts.Conflict(
			"specimen_already_released", "标本已放行离库")
	}
	handler, err := requiredText("经手人", handledBy, 80)
	if err != nil {
		return MoveRecord{}, err
	}
	why, err := requiredText("放行原因", reason, 240)
	if err != nil {
		return MoveRecord{}, err
	}
	stamp := now.UTC()
	record := MoveRecord{
		ID:           id,
		SpecimenID:   l.SpecimenID,
		Sequence:     len(l.Moves) + 1,
		FromLocation: l.CurrentLocation,
		ToLocation:   LocationReleased,
		HandledBy:    handler,
		Reason:       why,
		CreatedAt:    stamp,
	}
	l.Moves = append(l.Moves, record)
	l.CurrentLocation = LocationReleased
	l.UpdatedAt = stamp
	return record, nil
}

func parseStorageLocation(value, field string) (LocationCode, error) {
	code := LocationCode(strings.TrimSpace(value))
	if code == "" {
		return "", contracts.Invalid("required_field", field+"不能为空")
	}
	if _, ok := storageLocationCodes[code]; !ok {
		return "", contracts.Invalid("unknown_location", field+"不是受管库位")
	}
	return code, nil
}
