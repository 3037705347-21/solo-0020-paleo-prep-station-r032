package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var created domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
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
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_release", "存在未结束方案，不能办理研究放行")
		}
		if _, exists := m.ReleaseForSpecimen(specimenID); exists {
			return contracts.Conflict("release_exists", "该标本已经存在放行记录")
		}
		record, err := domain.NewReleaseRecord(
			s.nextID("release"),
			specimen,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkAvailable(s.now()); err != nil {
			return err
		}
		// 放行记录、标本终态与库位离库在同一事务提交，失败则整体回滚，
		// 不会出现"已 available 却仍显示在库"的矛盾快照。
		if _, err := ledger.CheckOut(
			s.nextID("move"),
			request.ApprovedBy,
			request.Purpose,
			s.now(),
		); err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		m.PutLocationLedger(ledger)
		created = record
		return nil
	})
	return created, err
}
