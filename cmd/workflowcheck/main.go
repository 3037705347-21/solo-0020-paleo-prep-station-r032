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
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
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

type moveView struct {
	ID           string `json:"id"`
	Sequence     int    `json:"sequence"`
	FromLocation string `json:"from_location"`
	ToLocation   string `json:"to_location"`
}

type moveResultView struct {
	Move     moveView `json:"move"`
	Replayed bool     `json:"replayed"`
}

type specimenDetailView struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	CurrentLocation string     `json:"current_location"`
	OnSite          bool       `json:"on_site"`
	Moves           []moveView `json:"moves"`
}

type specimenLocationView struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	CurrentLocation string `json:"current_location"`
	OnSite          bool   `json:"on_site"`
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

	case "track-location":
		return checkTrackLocation(client, server.URL)

	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkTrackLocation(client *http.Client, baseURL string) error {
	// 登记即落在地下库，且没有任何移动历史。
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}
	detail, err := getSpecimenDetail(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if detail.CurrentLocation != "underground_storage" || !detail.OnSite {
		return fmt.Errorf("new specimen should be on site at underground_storage, got %s on_site=%v", detail.CurrentLocation, detail.OnSite)
	}
	if len(detail.Moves) != 0 {
		return fmt.Errorf("new specimen must have no move records, got %d", len(detail.Moves))
	}

	// 地下库 -> 修复台。
	first, err := submitMove(client, baseURL, specimen.ID, contracts.MoveRequest{
		FromLocation: "underground_storage",
		ToLocation:   "prep_bench",
		HandledBy:    "preparator-chen",
		Reason:       "开始基质去除",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if first.Move.Sequence != 1 || first.Move.FromLocation != "underground_storage" ||
		first.Move.ToLocation != "prep_bench" || first.Replayed {
		return fmt.Errorf("unexpected first move result: %+v", first)
	}

	// 完全重复提交必须幂等回放：200、replayed=true、历史仍是 1 条。
	replay, err := submitMove(client, baseURL, specimen.ID, contracts.MoveRequest{
		FromLocation: "underground_storage",
		ToLocation:   "prep_bench",
		HandledBy:    "preparator-chen",
		Reason:       "开始基质去除",
	}, http.StatusOK)
	if err != nil {
		return err
	}
	if !replay.Replayed || replay.Move.ID != first.Move.ID {
		return fmt.Errorf("duplicate submission must replay the same move, got %+v", replay)
	}
	moves, err := listMoves(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(moves) != 1 || moves[0].Sequence != 1 {
		return fmt.Errorf("duplicate submission must not add history, got %+v", moves)
	}

	// 来源库位与当前库位不符必须冲突拒绝，且不留下记录。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/moves", contracts.MoveRequest{
		FromLocation: "underground_storage",
		ToLocation:   "cold_cabinet",
		HandledBy:    "keeper-zhou",
		Reason:       "来源过期",
	}, http.StatusConflict); err != nil {
		return err
	}
	// 来源正确但目标与当前相同（原地移动）必须冲突拒绝，且不留下记录。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/moves", contracts.MoveRequest{
		FromLocation: "prep_bench",
		ToLocation:   "prep_bench",
		HandledBy:    "preparator-chen",
		Reason:       "重复确认",
	}, http.StatusConflict); err != nil {
		return err
	}
	// 未知库位是输入错误，返回 400，且不留下记录。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/moves", contracts.MoveRequest{
		FromLocation: "prep_bench",
		ToLocation:   "display_case_9",
		HandledBy:    "preparator-chen",
		Reason:       "写错库位",
	}, http.StatusBadRequest); err != nil {
		return err
	}
	// 对未知标本的移动必须得到 404。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/specimen-unknown/moves", contracts.MoveRequest{
		FromLocation: "prep_bench",
		ToLocation:   "cold_cabinet",
		HandledBy:    "preparator-chen",
		Reason:       "幽灵标本",
	}, http.StatusNotFound); err != nil {
		return err
	}
	moves, err = listMoves(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(moves) != 1 {
		return fmt.Errorf("rejected moves must not leave partial records, history=%d", len(moves))
	}

	// 修复台 -> 冷柜。
	if _, err := submitMove(client, baseURL, specimen.ID, contracts.MoveRequest{
		FromLocation: "prep_bench",
		ToLocation:   "cold_cabinet",
		HandledBy:    "keeper-zhou",
		Reason:       "夜间控温保存",
	}, http.StatusCreated); err != nil {
		return err
	}
	// 回到原库位是合法的新交接，不能被当成重复提交吞掉。
	returned, err := submitMove(client, baseURL, specimen.ID, contracts.MoveRequest{
		FromLocation: "cold_cabinet",
		ToLocation:   "prep_bench",
		HandledBy:    "preparator-chen",
		Reason:       "继续修复",
	}, http.StatusCreated)
	if err != nil {
		return err
	}
	if returned.Move.Sequence != 3 || returned.Replayed {
		return fmt.Errorf("returning to a prior location must be recorded as sequence 3, got %+v", returned.Move)
	}

	// 列表与详情必须读取同一位置事实。
	detail, err = getSpecimenDetail(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if detail.CurrentLocation != "prep_bench" || !detail.OnSite {
		return fmt.Errorf("detail should show prep_bench on site, got %s on_site=%v", detail.CurrentLocation, detail.OnSite)
	}
	if len(detail.Moves) != 3 {
		return fmt.Errorf("detail history should keep all 3 moves, got %d", len(detail.Moves))
	}
	if detail.Moves[2].ToLocation != "prep_bench" {
		return fmt.Errorf("move history must not be overwritten by the current location, got %+v", detail.Moves)
	}
	listView, err := findInSpecimenList(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if listView.CurrentLocation != detail.CurrentLocation || listView.OnSite != detail.OnSite {
		return fmt.Errorf("list and detail disagree on location: %+v vs %+v", listView, detail)
	}

	// 完成制备并放行后，标本离库，不能再显示在任何库位内。
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK, &planView{},
	); err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}
	var release releaseView
	if err := requestJSON(
		client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "比较解剖研究",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated, &release,
	); err != nil {
		return err
	}
	detail, err = getSpecimenDetail(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if detail.CurrentLocation != "released" || detail.OnSite {
		return fmt.Errorf("released specimen must be off site, got %s on_site=%v", detail.CurrentLocation, detail.OnSite)
	}
	if len(detail.Moves) != 4 || detail.Moves[3].ToLocation != "released" {
		return fmt.Errorf("release must append one checkout move, got %+v", detail.Moves)
	}
	// 放行后的移动必须冲突拒绝，且不再追加任何记录。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/moves", contracts.MoveRequest{
		FromLocation: "prep_bench",
		ToLocation:   "temporary_display",
		HandledBy:    "keeper-zhou",
		Reason:       "临时展出",
	}, http.StatusConflict); err != nil {
		return err
	}
	// 放行后也不能再创建方案。
	if err := expectStatus(client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans", contracts.CreatePlanRequest{
		Objective:      "二次修复",
		Method:         "手工显微剥离",
		TargetExposure: 90,
	}, http.StatusConflict); err != nil {
		return err
	}
	detail, err = getSpecimenDetail(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if len(detail.Moves) != 4 {
		return fmt.Errorf("rejected post-release moves must not leave records, history=%d", len(detail.Moves))
	}
	listView, err = findInSpecimenList(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}
	if listView.CurrentLocation != "released" || listView.OnSite {
		return fmt.Errorf("released specimen must not appear on site in list, got %+v", listView)
	}
	return nil
}

func submitMove(
	client *http.Client,
	baseURL, specimenID string,
	request contracts.MoveRequest,
	wantStatus int,
) (moveResultView, error) {
	var result moveResultView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/moves",
		request,
		wantStatus,
		&result,
	)
	return result, err
}

func listMoves(client *http.Client, baseURL, specimenID string) ([]moveView, error) {
	var body struct {
		Items []moveView `json:"items"`
	}
	err := requestJSON(
		client,
		http.MethodGet,
		baseURL+"/specimens/"+specimenID+"/moves",
		nil,
		http.StatusOK,
		&body,
	)
	return body.Items, err
}

func getSpecimenDetail(client *http.Client, baseURL, specimenID string) (specimenDetailView, error) {
	var detail specimenDetailView
	err := requestJSON(
		client,
		http.MethodGet,
		baseURL+"/specimens/"+specimenID,
		nil,
		http.StatusOK,
		&detail,
	)
	return detail, err
}

func findInSpecimenList(client *http.Client, baseURL, specimenID string) (specimenLocationView, error) {
	var body struct {
		Items []specimenLocationView `json:"items"`
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens", nil, http.StatusOK, &body); err != nil {
		return specimenLocationView{}, err
	}
	for _, item := range body.Items {
		if item.ID == specimenID {
			return item, nil
		}
	}
	return specimenLocationView{}, fmt.Errorf("specimen %s missing from list", specimenID)
}

func expectStatus(
	client *http.Client,
	method, url string,
	body any,
	wantStatus int,
) error {
	return requestJSON(client, method, url, body, wantStatus, nil)
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
