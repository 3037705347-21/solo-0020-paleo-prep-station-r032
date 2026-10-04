package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID              string `json:"id"`
	CatalogNumber   string `json:"catalog_number"`
	Status          string `json:"status"`
	CurrentLocation string `json:"current_location"`
}

type specimenListView struct {
	Items []specimenView `json:"items"`
}

type movementView struct {
	ID           string `json:"id"`
	SpecimenID   string `json:"specimen_id"`
	RequestID    string `json:"request_id"`
	Sequence     int    `json:"sequence"`
	FromLocation string `json:"from_location"`
	ToLocation   string `json:"to_location"`
	MovedBy      string `json:"moved_by"`
	Reason       string `json:"reason"`
}

type movementListView struct {
	Items []movementView `json:"items"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "move-specimen":
		return checkMoveSpecimen(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

// checkMoveSpecimen 覆盖库位移动链路：登记落位、幂等重放、回到原库位、
// 历史保留、非法去向、未知标本以及放行离馆后的终态。
func checkMoveSpecimen(client *http.Client, baseURL string) error {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}
	if specimen.CurrentLocation != "underground_storage" {
		return fmt.Errorf("expected initial location underground_storage, got %s", specimen.CurrentLocation)
	}

	first, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-001",
		ToLocation: "repair_bench",
		MovedBy:    "keeper-zhou",
		Reason:     "送修复台清理",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if first.FromLocation != "underground_storage" || first.ToLocation != "repair_bench" || first.Sequence != 1 {
		return errors.New("first movement record does not reflect the handover")
	}

	replay, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-001",
		ToLocation: "repair_bench",
		MovedBy:    "keeper-zhou",
		Reason:     "送修复台清理",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	if replay.ID != first.ID {
		return errors.New("duplicate submission did not return the original movement record")
	}
	moves, err := listMovements(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(moves) != 1 {
		return fmt.Errorf("expected exactly one movement after duplicate submission, got %d", len(moves))
	}

	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-001",
		ToLocation: "repair_bench",
		MovedBy:    "keeper-zhou",
		Reason:     "改写原因",
	}, http.StatusConflict); err != nil {
		return err
	}

	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-002",
		ToLocation: "repair_bench",
		MovedBy:    "keeper-zhou",
		Reason:     "重复移动到当前库位",
	}, http.StatusConflict); err != nil {
		return err
	}

	if err := expectLocation(client, baseURL, specimen.ID, "repair_bench"); err != nil {
		return err
	}

	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, plan.ID, 40, "基质粗剥"); err != nil {
		return err
	}

	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-003",
		ToLocation: "cold_storage",
		MovedBy:    "keeper-zhou",
		Reason:     "暂停期间低温保存",
	}, http.StatusCreated); err != nil {
		return err
	}
	back, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-004",
		ToLocation: "underground_storage",
		MovedBy:    "keeper-zhou",
		Reason:     "回到地下库原位",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if back.FromLocation != "cold_storage" || back.ToLocation != "underground_storage" || back.Sequence != 3 {
		return errors.New("return-to-original-location movement is inconsistent")
	}

	moves, err = listMovements(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(moves) != 3 {
		return fmt.Errorf("expected three preserved movements, got %d", len(moves))
	}
	wantChain := [][2]string{
		{"underground_storage", "repair_bench"},
		{"repair_bench", "cold_storage"},
		{"cold_storage", "underground_storage"},
	}
	for i, want := range wantChain {
		if moves[i].Sequence != i+1 || moves[i].FromLocation != want[0] || moves[i].ToLocation != want[1] {
			return fmt.Errorf("movement history entry %d was overwritten or reordered", i+1)
		}
	}

	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-005",
		ToLocation: "released",
		MovedBy:    "keeper-zhou",
		Reason:     "绕过放行流程",
	}, http.StatusBadRequest); err != nil {
		return err
	}
	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-006",
		ToLocation: "unknown-place",
		MovedBy:    "keeper-zhou",
		Reason:     "不存在的位置",
	}, http.StatusBadRequest); err != nil {
		return err
	}
	if _, err := moveSpecimen(client, baseURL, "specimen-missing", contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-007",
		ToLocation: "cold_storage",
		MovedBy:    "keeper-zhou",
		Reason:     "未知标本",
	}, http.StatusNotFound); err != nil {
		return err
	}
	var ignored struct{}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/specimen-missing/moves", nil, http.StatusNotFound, &ignored); err != nil {
		return err
	}

	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}
	var release releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "比较解剖研究",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		&release,
	); err != nil {
		return err
	}
	if err := expectLocation(client, baseURL, specimen.ID, "released"); err != nil {
		return err
	}
	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "available" {
		return fmt.Errorf("expected available specimen, got %s", fetched.Status)
	}

	moves, err = listMovements(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(moves) != 4 {
		return fmt.Errorf("expected release to append a fourth movement, got %d", len(moves))
	}
	last := moves[3]
	if last.Sequence != 4 || last.FromLocation != "underground_storage" || last.ToLocation != "released" || last.MovedBy != "keeper-zhou" {
		return errors.New("release handover movement is missing or inconsistent")
	}

	if _, err := moveSpecimen(client, baseURL, specimen.ID, contracts.MoveSpecimenRequest{
		RequestID:  "REQ-MOVE-008",
		ToLocation: "repair_bench",
		MovedBy:    "keeper-zhou",
		Reason:     "已放行仍尝试移动",
	}, http.StatusConflict); err != nil {
		return err
	}
	return nil
}

func moveSpecimen(
	client *http.Client,
	baseURL string,
	specimenID string,
	request contracts.MoveSpecimenRequest,
	wantStatus int,
) (movementView, error) {
	var movement movementView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/moves",
		request,
		wantStatus,
		&movement,
	)
	return movement, err
}

func listMovements(client *http.Client, baseURL, specimenID string) ([]movementView, error) {
	var list movementListView
	err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimenID+"/moves", nil, http.StatusOK, &list)
	return list.Items, err
}

// expectLocation 确认详情与列表读取到同一个当前库位事实。
func expectLocation(client *http.Client, baseURL, specimenID, want string) error {
	var detail specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimenID, nil, http.StatusOK, &detail); err != nil {
		return err
	}
	if detail.CurrentLocation != want {
		return fmt.Errorf("detail shows location %s, want %s", detail.CurrentLocation, want)
	}
	var list specimenListView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens", nil, http.StatusOK, &list); err != nil {
		return err
	}
	for _, item := range list.Items {
		if item.ID == specimenID {
			if item.CurrentLocation != want {
				return fmt.Errorf("list shows location %s, want %s", item.CurrentLocation, want)
			}
			return nil
		}
	}
	return errors.New("specimen missing from list response")
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
