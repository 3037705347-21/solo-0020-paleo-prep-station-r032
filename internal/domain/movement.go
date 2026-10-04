package domain

import (
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// MovementRecord 是一次库位交接的不可变事实。来源取自交接前的当前库位，
// 记录追加后永不被后续移动覆盖。
type MovementRecord struct {
	ID           string    `json:"id"`
	SpecimenID   string    `json:"specimen_id"`
	RequestID    string    `json:"request_id"`
	Sequence     int       `json:"sequence"`
	FromLocation Location  `json:"from_location"`
	ToLocation   Location  `json:"to_location"`
	MovedBy      string    `json:"moved_by"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
}

func NewMovementRecord(
	id string,
	specimen Specimen,
	previous []MovementRecord,
	requestID string,
	to Location,
	movedBy string,
	reason string,
	now time.Time,
) (MovementRecord, error) {
	if id == "" {
		return MovementRecord{}, contracts.Internal("missing_generated_id", "移动标识未生成", nil)
	}
	request, err := requiredText("请求标识", requestID, 80)
	if err != nil {
		return MovementRecord{}, err
	}
	handler, err := requiredText("经手人", movedBy, 80)
	if err != nil {
		return MovementRecord{}, err
	}
	cause, err := requiredText("移动原因", reason, 200)
	if err != nil {
		return MovementRecord{}, err
	}
	return MovementRecord{
		ID:           id,
		SpecimenID:   specimen.ID,
		RequestID:    request,
		Sequence:     len(previous) + 1,
		FromLocation: specimen.CurrentLocation,
		ToLocation:   to,
		MovedBy:      handler,
		Reason:       cause,
		CreatedAt:    now.UTC(),
	}, nil
}

// MatchesRequest 判断已有记录是否与重复提交的请求内容一致，
// 用于幂等回放与冲突识别。
func (m MovementRecord) MatchesRequest(to Location, movedBy string, reason string) bool {
	return m.ToLocation == to &&
		m.MovedBy == strings.TrimSpace(movedBy) &&
		m.Reason == strings.TrimSpace(reason)
}
