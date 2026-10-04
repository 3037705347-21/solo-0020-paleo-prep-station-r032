package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/repository"
)

// MoveSpecimen 登记一次库位交接。
//
// 当前库位和移动历史在同一份库位台账上、同一个仓储事务中提交：
// 任何校验失败都在提交前返回，不会留下只更新了位置或只追加了记录的中间状态。
// 与上一条交接完全相同的提交按重复提交回放既有记录（Replayed=true），
// 不会产生第二条记录。
func (s *Service) MoveSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.MoveRequest,
) (MoveResult, error) {
	if err := ctx.Err(); err != nil {
		return MoveResult{}, err
	}

	var result MoveResult
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		ledger, ok := m.LocationLedgerForSpecimen(specimenID)
		if !ok {
			return contracts.Internal(
				"location_ledger_missing",
				"标本缺少库位台账 "+specimenID,
				nil,
			)
		}
		record, replayed, err := ledger.RecordMove(
			s.nextID("move"),
			request.FromLocation,
			request.ToLocation,
			request.HandledBy,
			request.Reason,
			s.now(),
		)
		if err != nil {
			return err
		}
		if !replayed {
			m.PutLocationLedger(ledger)
		}
		result = MoveResult{Move: record, Replayed: replayed}
		return nil
	})
	return result, err
}
