// Package report implements the daily/weekly/monthly report engine for
// aquaculture farm analytics. It aggregates data from the store layer into
// structured, exportable reports (JSON/CSV) covering feeding stats, growth
// delta, water quality trends, and yield/FCR metrics.
//
// Architecture:
//   - Store: pluggable data-source interface (mockable, no live DB required).
//   - Engine: orchestrates report generation per ReportType.
//   - Report/Section/Metric: chain-style builder for composing reports.
//
// Export guard: reports exceeding MaxExportRows rows are rejected with
// ErrExportLimit to prevent oversized responses (per README §5.10).
package report

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrInvalidFarmID is returned when the farm ID is empty.
	ErrInvalidFarmID = errors.New("report: invalid farm ID")
	// ErrInvalidReportType is returned for unsupported report types.
	ErrInvalidReportType = errors.New("report: invalid report type")
	// ErrInvalidPeriod is returned when From/To are unordered.
	ErrInvalidPeriod = errors.New("report: invalid reporting period")
	// ErrExportLimit is returned when the report exceeds MaxExportRows.
	ErrExportLimit = errors.New("report: export exceeds row limit")
)

// MaxExportRows caps the number of metric rows that may be exported.
const MaxExportRows = 10_000

// ============================================================================
// Report type
// ============================================================================

// ReportType enumerates supported report kinds.
type ReportType string

const (
	// Daily is the daily production report.
	Daily ReportType = "daily"
	// Weekly is the weekly growth report.
	Weekly ReportType = "weekly"
	// Monthly is the monthly performance report.
	Monthly ReportType = "monthly"
)

// ValidateType reports whether rt is a supported report type.
func ValidateType(rt ReportType) error {
	switch rt {
	case Daily, Weekly, Monthly:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidReportType, rt)
	}
}

// ============================================================================
// Store interface
// ============================================================================

// DataPoint is a per-day aggregate row returned by a Store implementation.
type DataPoint struct {
	Day time.Time
	// Value carries the measured/aggregated numeric value.
	Value float64
}

// FeedingStat is a daily feeding total in grams.
type FeedingStat struct {
	Day       time.Time
	FeedGrams float64
}

// GrowthRecord is a daily average fish weight sample in grams.
type GrowthRecord struct {
	Day         time.Time
	AvgWeightG float64
}

// WaterQualitySample is a daily average of water-quality probes.
type WaterQualitySample struct {
	Day    time.Time
	DO     float64 // dissolved oxygen, mg/L
	PH     float64
	TempC  float64
	NH3    float64 // ammonia nitrogen, mg/L
	Turbid float64 // turbidity, NTU
}

// YieldFCR bundles end-of-period yield and feed conversion ratio.
type YieldFCR struct {
	YieldKg float64
	FCR     float64
}

// Store is the data source abstraction for the report engine.
//
// Implementations must be safe for concurrent use.
type Store interface {
	// FeedingStats returns daily feeding totals within [from, to].
	FeedingStats(ctx context.Context, farmID string, from, to time.Time) ([]FeedingStat, error)
	// GrowthRecords returns daily average-weight samples within [from, to].
	GrowthRecords(ctx context.Context, farmID string, from, to time.Time) ([]GrowthRecord, error)
	// WaterQuality returns daily water-quality averages within [from, to].
	WaterQuality(ctx context.Context, farmID string, from, to time.Time) ([]WaterQualitySample, error)
	// YieldAndFCR returns yield/FCR aggregates for the period.
	YieldAndFCR(ctx context.Context, farmID string, from, to time.Time) (YieldFCR, error)
}

// ============================================================================
// Params & Engine
// ============================================================================

// Params scopes a report generation request.
type Params struct {
	From time.Time
	To   time.Time
}

// Engine generates reports from a Store.
type Engine struct {
	store Store
}

// NewEngine creates a report Engine over the given Store.
func NewEngine(store Store) *Engine {
	return &Engine{store: store}
}

// GenerateReport builds a Report of the requested type for the farm.
//
// Daily includes feeding stats and water quality trends. Weekly adds growth
// delta. Monthly further adds yield/FCR metrics.
func (e *Engine) GenerateReport(ctx context.Context, farmID string, rt ReportType, p Params) (*Report, error) {
	if farmID == "" {
		return nil, ErrInvalidFarmID
	}
	if err := ValidateType(rt); err != nil {
		return nil, err
	}
	if !p.From.Before(p.To) {
		return nil, ErrInvalidPeriod
	}
	if e.store == nil {
		return nil, fmt.Errorf("report: store is nil")
	}

	feeding, err := e.store.FeedingStats(ctx, farmID, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("report: feeding stats: %w", err)
	}
	water, err := e.store.WaterQuality(ctx, farmID, p.From, p.To)
	if err != nil {
		return nil, fmt.Errorf("report: water quality: %w", err)
	}

	rpt := NewReport(rt, farmID)
	rpt.Period = p
	rpt.addFeedingSection(feeding)
	rpt.addWaterSection(water)

	if rt == Weekly || rt == Monthly {
		growth, err := e.store.GrowthRecords(ctx, farmID, p.From, p.To)
		if err != nil {
			return nil, fmt.Errorf("report: growth records: %w", err)
		}
		rpt.addGrowthSection(growth)
	}
	if rt == Monthly {
		yf, err := e.store.YieldAndFCR(ctx, farmID, p.From, p.To)
		if err != nil {
			return nil, fmt.Errorf("report: yield/fcr: %w", err)
		}
		rpt.addYieldSection(yf)
	}
	return rpt, nil
}

func (r *Report) addFeedingSection(stats []FeedingStat) {
	s := r.AddSection("Feeding")
	var total float64
	for _, f := range stats {
		total += f.FeedGrams
	}
	s.AddMetric("feed_total_g", total, "g")
	if len(stats) > 0 {
		avg := total / float64(len(stats))
		s.AddMetric("feed_avg_g_per_day", avg, "g/day")
		s.AddMetric("feeding_days", float64(len(stats)), "count")
	}
}

func (r *Report) addWaterSection(samples []WaterQualitySample) {
	s := r.AddSection("Water Quality")
	if len(samples) == 0 {
		return
	}
	var do, ph, temp, nh3, turb, n float64
	for _, w := range samples {
		do += w.DO
		ph += w.PH
		temp += w.TempC
		nh3 += w.NH3
		turb += w.Turbid
		n++
	}
	s.AddMetric("avg_do", do/n, "mg/L")
	s.AddMetric("avg_ph", ph/n, "")
	s.AddMetric("avg_temp_c", temp/n, "C")
	s.AddMetric("avg_nh3", nh3/n, "mg/L")
	s.AddMetric("avg_turbidity_ntu", turb/n, "NTU")
}

func (r *Report) addGrowthSection(records []GrowthRecord) {
	s := r.AddSection("Growth")
	if len(records) < 2 {
		return
	}
	first := records[0].AvgWeightG
	last := records[len(records)-1].AvgWeightG
	s.AddMetric("growth_delta_g", last-first, "g")
	s.AddMetric("samples", float64(len(records)), "count")
}

func (r *Report) addYieldSection(yf YieldFCR) {
	s := r.AddSection("Yield & FCR")
	s.AddMetric("yield_kg", yf.YieldKg, "kg")
	s.AddMetric("fcr", yf.FCR, "")
}

// ============================================================================
// Report builder
// ============================================================================

// Metric is a single named, unit-qualified measurement.
type Metric struct {
	Key   string  `json:"key"`
	Label string  `json:"label,omitempty"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

// Section groups related metrics under a title.
type Section struct {
	Title   string   `json:"title"`
	Metrics []Metric `json:"metrics"`
}

// AddMetric appends a metric to the section and returns the section for
// chain-style building.
func (s *Section) AddMetric(key string, value float64, unit string) *Section {
	s.Metrics = append(s.Metrics, Metric{Key: key, Value: value, Unit: unit})
	return s
}

// Report is the materialized output of the report engine.
type Report struct {
	Type        ReportType `json:"type"`
	FarmID      string     `json:"farm_id"`
	GeneratedAt time.Time  `json:"generated_at"`
	Period      Params     `json:"period"`
	Sections    []*Section `json:"sections"`
}

// NewReport creates an empty report for the given farm.
func NewReport(rt ReportType, farmID string) *Report {
	return &Report{
		Type:        rt,
		FarmID:      farmID,
		GeneratedAt: time.Now().UTC(),
		Sections:    make([]*Section, 0),
	}
}

// AddSection appends a new section and returns it for chain building.
func (r *Report) AddSection(title string) *Section {
	s := &Section{Title: title, Metrics: make([]Metric, 0)}
	r.Sections = append(r.Sections, s)
	return s
}

// rowCount sums the metric rows across all sections.
func (r *Report) rowCount() int {
	n := 0
	for _, s := range r.Sections {
		n += len(s.Metrics)
	}
	return n
}

// checkExportLimit guards against oversized exports.
func (r *Report) checkExportLimit() error {
	if r.rowCount() > MaxExportRows {
		return fmt.Errorf("%w: %d rows > %d", ErrExportLimit, r.rowCount(), MaxExportRows)
	}
	return nil
}

// ============================================================================
// Export
// ============================================================================

// ExportJSON writes the report as indented JSON.
func ExportJSON(r *Report, w io.Writer) error {
	if err := r.checkExportLimit(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// ExportCSV writes a flat section,key,value,unit dump of the report.
func ExportCSV(r *Report, w io.Writer) error {
	if err := r.checkExportLimit(); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"section", "metric", "value", "unit"}); err != nil {
		return err
	}
	for _, s := range r.Sections {
		for _, m := range s.Metrics {
			rec := []string{s.Title, m.Key, fmt.Sprintf("%v", m.Value), m.Unit}
			if err := cw.Write(rec); err != nil {
				return err
			}
		}
	}
	return cw.Error()
}