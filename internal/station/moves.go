package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// MoveSpecimen 记录一次库位交接，并在同一仓储事务中把标本的当前库位
// 推进到去向；任一步失败都不会留下半条移动记录。返回的布尔值表示
// 本次是否真正写入了新记录，重复提交同一请求标识时返回 false 和首次记录。
func (s *Service) MoveSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.MoveSpecimenRequest,
) (domain.MovementRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.MovementRecord{}, false, err
	}
	to, err := domain.ParseStorageLocation(request.ToLocation)
	if err != nil {
		return domain.MovementRecord{}, false, err
	}

	var record domain.MovementRecord
	created := false
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if existing, found := m.MovementForRequest(specimenID, request.RequestID); found {
			if !existing.MatchesRequest(to, request.MovedBy, request.Reason) {
				return contracts.Conflict("move_request_conflict", "相同请求标识的移动记录内容不一致")
			}
			record = existing
			return nil
		}
		movement, err := domain.NewMovementRecord(
			s.nextID("move"),
			specimen,
			m.MovementsForSpecimen(specimenID),
			request.RequestID,
			to,
			request.MovedBy,
			request.Reason,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := specimen.ApplyMove(to, s.now()); err != nil {
			return err
		}
		m.AppendMovement(movement)
		m.PutSpecimen(specimen)
		record = movement
		created = true
		return nil
	})
	return record, created, err
}

func (s *Service) ListMovements(
	ctx context.Context,
	specimenID string,
) ([]domain.MovementRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.MovementRecord
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		values = m.MovementsForSpecimen(specimenID)
		return nil
	})
	return values, err
}
