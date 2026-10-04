package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

type SpecimenStatus string

const (
	SpecimenRegistered SpecimenStatus = "registered"
	SpecimenPlanned    SpecimenStatus = "planned"
	SpecimenPreparing  SpecimenStatus = "preparing"
	SpecimenStabilized SpecimenStatus = "stabilized"
	SpecimenAvailable  SpecimenStatus = "available"
)

type Specimen struct {
	ID              string         `json:"id"`
	CatalogNumber   string         `json:"catalog_number"`
	FieldLabel      string         `json:"field_label"`
	TaxonHint       string         `json:"taxon_hint"`
	MatrixKind      string         `json:"matrix_kind"`
	JacketNotes     string         `json:"jacket_notes"`
	Status          SpecimenStatus `json:"status"`
	CurrentLocation Location       `json:"current_location"`
	Version         int            `json:"version"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func NewSpecimen(
	id string,
	catalogNumber string,
	fieldLabel string,
	taxonHint string,
	matrixKind string,
	jacketNotes string,
	now time.Time,
) (Specimen, error) {
	if id == "" {
		return Specimen{}, contracts.Internal("missing_generated_id", "标本标识未生成", nil)
	}
	catalog, err := validateCatalogNumber(catalogNumber)
	if err != nil {
		return Specimen{}, err
	}
	field, err := requiredText("野外标签", fieldLabel, 80)
	if err != nil {
		return Specimen{}, err
	}
	taxon, err := optionalText("分类提示", taxonHint, 120)
	if err != nil {
		return Specimen{}, err
	}
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return Specimen{}, err
	}
	notes, err := optionalText("夹克备注", jacketNotes, 500)
	if err != nil {
		return Specimen{}, err
	}
	return Specimen{
		ID:              id,
		CatalogNumber:   catalog,
		FieldLabel:      field,
		TaxonHint:       taxon,
		MatrixKind:      matrix,
		JacketNotes:     notes,
		Status:          SpecimenRegistered,
		CurrentLocation: LocationUndergroundStorage,
		Version:         1,
		CreatedAt:       now.UTC(),
		UpdatedAt:       now.UTC(),
	}, nil
}

func (s Specimen) CanCreatePlan() bool {
	return s.Status == SpecimenRegistered || s.Status == SpecimenPlanned
}

func (s Specimen) IsStable() bool {
	return s.Status == SpecimenStabilized
}

func (s *Specimen) MarkPlanned(now time.Time) error {
	if s.Status == SpecimenPlanned {
		return nil
	}
	if s.Status != SpecimenRegistered {
		return contracts.Conflict("specimen_not_plannable", "当前标本状态不能创建修复方案")
	}
	s.Status = SpecimenPlanned
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkPreparing(now time.Time) error {
	if s.Status == SpecimenPreparing {
		return nil
	}
	if s.Status != SpecimenPlanned {
		return contracts.Conflict("specimen_not_preparing", "只有已规划标本才能进入制备状态")
	}
	s.Status = SpecimenPreparing
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkStabilized(now time.Time) error {
	if s.Status == SpecimenStabilized {
		return nil
	}
	if s.Status != SpecimenPreparing {
		return contracts.Conflict("specimen_not_stabilizable", "只有正在制备的标本才能进入稳定状态")
	}
	s.Status = SpecimenStabilized
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

func (s *Specimen) MarkAvailable(now time.Time) error {
	if s.Status != SpecimenStabilized {
		return contracts.Conflict("specimen_not_releasable", "只有已稳定标本可以办理放行")
	}
	s.Status = SpecimenAvailable
	s.CurrentLocation = LocationReleased
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}

// ApplyMove 把当前库位推进到目标库位。已放行标本已经离馆，不能再在
// 库位间移动；目标与当前库位相同的移动没有交接意义，直接拒绝。
func (s *Specimen) ApplyMove(to Location, now time.Time) error {
	if s.Status == SpecimenAvailable {
		return contracts.Conflict("specimen_not_movable", "已放行标本不能在库位间移动")
	}
	if to == LocationReleased {
		return contracts.Conflict("specimen_not_movable", "放行出库只能由研究放行流程完成")
	}
	if s.CurrentLocation == to {
		return contracts.Conflict("already_at_location", "标本已在目标库位，不能重复移动")
	}
	s.CurrentLocation = to
	s.Version++
	s.UpdatedAt = now.UTC()
	return nil
}
