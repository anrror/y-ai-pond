package report

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

var testFrom = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
var testTo = time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)

var _ Store = (*mockStore)(nil)

type mockStore struct {
	feeding  []FeedingStat
	growth   []GrowthRecord
	water    []WaterQualitySample
	yieldFCR YieldFCR
	err      error
}

func (m *mockStore) FeedingStats(_ context.Context, _ string, _, _ time.Time) ([]FeedingStat, error) {
	return m.feeding, m.err
}

func (m *mockStore) GrowthRecords(_ context.Context, _ string, _, _ time.Time) ([]GrowthRecord, error) {
	return m.growth, m.err
}

func (m *mockStore) WaterQuality(_ context.Context, _ string, _, _ time.Time) ([]WaterQualitySample, error) {
	return m.water, m.err
}

func (m *mockStore) YieldAndFCR(_ context.Context, _ string, _, _ time.Time) (YieldFCR, error) {
	return m.yieldFCR, m.err
}

func TestGenerateReportDaily(t *testing.T) {
	store := &mockStore{
		feeding: []FeedingStat{{Day: testFrom.Add(24 * time.Hour), FeedGrams: 1500}},
		water:   []WaterQualitySample{{Day: testFrom.Add(24 * time.Hour), DO: 7.2, PH: 7.8, TempC: 25.0}},
	}
	engine := NewEngine(store)
	rpt, err := engine.GenerateReport(context.Background(), "farm-1", Daily, Params{From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if rpt.Type != Daily {
		t.Fatalf("expected type daily, got %v", rpt.Type)
	}
	if rpt.FarmID != "farm-1" {
		t.Fatalf("expected farm-1, got %s", rpt.FarmID)
	}
	if len(rpt.Sections) < 2 {
		t.Fatalf("expected >=2 sections, got %d", len(rpt.Sections))
	}
	foundFeeding := false
	foundWater := false
	for _, s := range rpt.Sections {
		switch s.Title {
		case "Feeding":
			foundFeeding = len(s.Metrics) > 0
		case "Water Quality":
			foundWater = len(s.Metrics) > 0
		}
	}
	if !foundFeeding {
		t.Fatal("expected Feeding section with metrics")
	}
	if !foundWater {
		t.Fatal("expected Water Quality section with metrics")
	}
}

func TestGenerateReportWeeklyGrowth(t *testing.T) {
	store := &mockStore{
		feeding: []FeedingStat{{Day: testFrom, FeedGrams: 9000}},
		growth:  []GrowthRecord{{Day: testFrom, AvgWeightG: 400.0}, {Day: testTo, AvgWeightG: 460.0}},
		water:   []WaterQualitySample{{Day: testFrom, DO: 7.0, PH: 7.5, TempC: 24.0}},
	}
	engine := NewEngine(store)
	rpt, err := engine.GenerateReport(context.Background(), "farm-1", Weekly, Params{From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	foundGrowth := false
	for _, s := range rpt.Sections {
		if s.Title == "Growth" {
			for _, m := range s.Metrics {
				if m.Key == "growth_delta_g" && m.Value == 60.0 {
					foundGrowth = true
				}
			}
		}
	}
	if !foundGrowth {
		t.Fatalf("expected Growth section with growth_delta_g=60, sections=%d", len(rpt.Sections))
	}
}

func TestGenerateReportMonthlyYieldFCR(t *testing.T) {
	store := &mockStore{
		feeding:  []FeedingStat{{Day: testFrom, FeedGrams: 450000}},
		growth:   []GrowthRecord{{Day: testFrom, AvgWeightG: 300.0}, {Day: testTo, AvgWeightG: 500.0}},
		water:    []WaterQualitySample{{Day: testFrom, DO: 7.0, PH: 7.5, TempC: 24.0}},
		yieldFCR: YieldFCR{YieldKg: 1200.0, FCR: 1.8},
	}
	engine := NewEngine(store)
	rpt, err := engine.GenerateReport(context.Background(), "farm-1", Monthly, Params{From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	foundYield := false
	for _, s := range rpt.Sections {
		if s.Title == "Yield & FCR" {
			for _, m := range s.Metrics {
				if m.Key == "yield_kg" && m.Value == 1200.0 {
					foundYield = true
				}
				if m.Key == "fcr" && m.Value == 1.8 {
					foundYield = true
				}
			}
		}
	}
	if !foundYield {
		t.Fatalf("expected Yield & FCR section with yield_kg/fcr")
	}
}

func TestReportBuilderChain(t *testing.T) {
	rpt := NewReport(Daily, "farm-1")
	rpt.
		AddSection("Feeding").
		AddMetric("feed_total_kg", 12.5, "kg").
		AddMetric("feed_events", 42, "count")
	rpt.AddSection("Water").AddMetric("avg_do", 7.1, "mg/L")

	if len(rpt.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(rpt.Sections))
	}
	if len(rpt.Sections[0].Metrics) != 2 {
		t.Fatalf("expected 2 metrics in Feeding, got %d", len(rpt.Sections[0].Metrics))
	}
	if rpt.Sections[0].Metrics[0].Key != "feed_total_kg" || rpt.Sections[0].Metrics[0].Value != 12.5 {
		t.Fatalf("metric fields wrong: %+v", rpt.Sections[0].Metrics[0])
	}
}

func TestExportJSON(t *testing.T) {
	store := &mockStore{
		feeding: []FeedingStat{{Day: testFrom, FeedGrams: 1000}},
		water:   []WaterQualitySample{{Day: testFrom, DO: 7.0, PH: 7.5, TempC: 24.0}},
	}
	engine := NewEngine(store)
	rpt, err := engine.GenerateReport(context.Background(), "farm-1", Daily, Params{From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var sb strings.Builder
	if err := ExportJSON(rpt, &sb); err != nil {
		t.Fatalf("export json: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, `"farm_id"`) && !strings.Contains(out, "farm-1") {
		t.Fatalf("json missing farm id: %s", out)
	}
	if !strings.Contains(out, "Feeding") {
		t.Fatalf("json missing section: %s", out)
	}
}

func TestExportCSV(t *testing.T) {
	store := &mockStore{
		feeding: []FeedingStat{{Day: testFrom, FeedGrams: 1000}},
		water:   []WaterQualitySample{{Day: testFrom, DO: 7.0, PH: 7.5, TempC: 24.0}},
	}
	engine := NewEngine(store)
	rpt, err := engine.GenerateReport(context.Background(), "farm-1", Daily, Params{From: testFrom, To: testTo})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var sb strings.Builder
	err = ExportCSV(rpt, &sb)
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	rd := csv.NewReader(bufio.NewReader(strings.NewReader(sb.String())))
	rows, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("csv empty")
	}
	header := rows[0]
	if len(header) < 3 {
		t.Fatalf("csv header too narrow: %v", header)
	}
}

func TestExportLimitGuard(t *testing.T) {
	builder := NewReport(Daily, "farm-1")
	for i := 0; i <= MaxExportRows; i++ {
		builder.AddSection("S").AddMetric("m", float64(i), "u")
	}
	rpt := builder
	var sb strings.Builder
	err := ExportJSON(rpt, &sb)
	if !errors.Is(err, ErrExportLimit) {
		t.Fatalf("expected ErrExportLimit, got %v", err)
	}
	err = ExportCSV(rpt, &sb)
	if !errors.Is(err, ErrExportLimit) {
		t.Fatalf("expected ErrExportLimit (csv), got %v", err)
	}
}

func TestInvalidReportType(t *testing.T) {
	store := &mockStore{}
	engine := NewEngine(store)
	_, err := engine.GenerateReport(context.Background(), "farm-1", ReportType("yearly"), Params{From: testFrom, To: testTo})
	if !errors.Is(err, ErrInvalidReportType) {
		t.Fatalf("expected ErrInvalidReportType, got %v", err)
	}
}

func TestInvalidFarmID(t *testing.T) {
	store := &mockStore{}
	engine := NewEngine(store)
	_, err := engine.GenerateReport(context.Background(), "", Daily, Params{From: testFrom, To: testTo})
	if !errors.Is(err, ErrInvalidFarmID) {
		t.Fatalf("expected ErrInvalidFarmID, got %v", err)
	}
}

func TestStoreErrorPropagation(t *testing.T) {
	wantErr := errors.New("db down")
	store := &mockStore{err: wantErr}
	engine := NewEngine(store)
	_, err := engine.GenerateReport(context.Background(), "farm-1", Daily, Params{From: testFrom, To: testTo})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected store error propagation, got %v", err)
	}
}

func TestValidateType(t *testing.T) {
	if err := ValidateType(Daily); err != nil {
		t.Fatalf("daily should be valid: %v", err)
	}
	if err := ValidateType(Weekly); err != nil {
		t.Fatalf("weekly should be valid: %v", err)
	}
	if err := ValidateType(Monthly); err != nil {
		t.Fatalf("monthly should be valid: %v", err)
	}
	if err := ValidateType("hourly"); !errors.Is(err, ErrInvalidReportType) {
		t.Fatalf("expected ErrInvalidReportType, got %v", err)
	}
}