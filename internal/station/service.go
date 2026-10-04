package station

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

type Service struct {
	repo *repository.MemoryRepository
	now  func() time.Time
	seq  atomic.Uint64
}

func New(repo *repository.MemoryRepository) *Service {
	if repo == nil {
		repo = repository.NewMemory()
	}
	return &Service{
		repo: repo,
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) nextID(prefix string) string {
	var random [6]byte
	if _, err := rand.Read(random[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(random[:])
	}
	local := s.seq.Add(1)
	return prefix + "-" + time.Now().UTC().Format("20060102150405") + "-" + hex.EncodeToString([]byte{byte(local)})
}

type PlanSnapshot struct {
	Plan     domain.PrepPlan        `json:"plan"`
	Progress []domain.ProgressEntry `json:"progress"`
}

// SpecimenLocationView 是列表投影：标本快照加上来自同一台账的当前库位事实。
type SpecimenLocationView struct {
	domain.Specimen
	CurrentLocation domain.LocationCode `json:"current_location"`
	OnSite          bool                `json:"on_site"`
}

// SpecimenDetail 是详情投影：当前库位与完整移动历史都来自同一份库位台账，
// 历史只能被追加，不会被当前位置覆盖。
type SpecimenDetail struct {
	domain.Specimen
	CurrentLocation domain.LocationCode `json:"current_location"`
	OnSite          bool                `json:"on_site"`
	Moves           []domain.MoveRecord `json:"moves"`
}

// MoveResult 描述一次移动提交：replayed 为 true 时表示命中了幂等重放，
// 移动历史没有新增条目。
type MoveResult struct {
	Move     domain.MoveRecord `json:"move"`
	Replayed bool              `json:"replayed"`
}

func emptyMovesIfNil(moves []domain.MoveRecord) []domain.MoveRecord {
	if moves == nil {
		return []domain.MoveRecord{}
	}
	return moves
}

func (s *Service) ListSpecimens(ctx context.Context) ([]SpecimenLocationView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []SpecimenLocationView
	err := s.repo.Read(func(m *repository.Mutation) error {
		specimens := m.ListSpecimens()
		values = make([]SpecimenLocationView, 0, len(specimens))
		for _, specimen := range specimens {
			ledger, ok := m.LocationLedgerForSpecimen(specimen.ID)
			if !ok {
				// 每份标本都在登记事务中与台账成对创建；缺失即内部一致性损坏。
				return contracts.Internal(
					"location_ledger_missing",
					"标本缺少库位台账 "+specimen.ID,
					nil,
				)
			}
			values = append(values, SpecimenLocationView{
				Specimen:        specimen,
				CurrentLocation: ledger.CurrentLocation,
				OnSite:          ledger.IsOnSite(),
			})
		}
		return nil
	})
	return values, err
}

func (s *Service) GetSpecimen(ctx context.Context, specimenID string) (SpecimenDetail, error) {
	if err := ctx.Err(); err != nil {
		return SpecimenDetail{}, err
	}
	var value SpecimenDetail
	err := s.repo.Read(func(m *repository.Mutation) error {
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
		value = SpecimenDetail{
			Specimen:        specimen,
			CurrentLocation: ledger.CurrentLocation,
			OnSite:          ledger.IsOnSite(),
			Moves:           emptyMovesIfNil(ledger.Moves),
		}
		return nil
	})
	return value, err
}

func (s *Service) ListMoves(ctx context.Context, specimenID string) ([]domain.MoveRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.MoveRecord
	err := s.repo.Read(func(m *repository.Mutation) error {
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
		values = emptyMovesIfNil(ledger.Moves)
		return nil
	})
	return values, err
}
