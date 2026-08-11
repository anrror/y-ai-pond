package growth

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

//go:embed species.csv
var speciesCSVData []byte

// SpeciesParams contains the full parameter set for the Fish Bioenergetics 4.0
// model plus VBGM parameters for a single species.
//
// Parameter meanings follow Deslauriers et al. (2017) "Fish Bioenergetics 4.0:
// An R-Based Modeling Application" conventions.
type SpeciesParams struct {
	// Species is the canonical species name (e.g., "tilapia", "atlantic_salmon").
	Species string `json:"species"`

	// CommonName is the human-readable common name.
	CommonName string `json:"common_name"`

	// ===================================================================
	// VBGM parameters (von Bertalanffy Growth Model)
	// ===================================================================

	// Linf is the asymptotic maximum length (cm).
	Linf float64 `json:"linf_cm"`
	// K is the VBGM growth rate coefficient (per day).
	K float64 `json:"k"`
	// T0 is the theoretical age at zero length (days).
	T0 float64 `json:"t0"`
	// A is the length-weight coefficient: W = A * L^B.
	A float64 `json:"a"`
	// B is the length-weight exponent: W = A * L^B.
	B float64 `json:"b"`

	// ===================================================================
	// Consumption parameters
	// ===================================================================

	// Cmax is the maximum specific consumption rate (g prey / g fish / day)
	// for a 1g fish at optimal temperature.
	Cmax float64 `json:"cmax"`
	// BC is the allometric exponent for consumption: W^(BC-1).
	BC float64 `json:"bc"`
	// TOpt is the optimal temperature for consumption (°C).
	TOpt float64 `json:"t_opt"`
	// TMaxC is the upper lethal temperature for consumption (°C).
	TMaxC float64 `json:"t_max_c"`
	// CK1 is the rising-limb exponent for the consumption temperature curve.
	CK1 float64 `json:"ck1"`
	// CK2 is the falling-limb exponent for the consumption temperature curve.
	CK2 float64 `json:"ck2"`

	// ===================================================================
	// Respiration (metabolism) parameters
	// ===================================================================

	// Rmax is the maximum specific respiration rate (J / g / day)
	// for a 1g fish at the reference temperature.
	Rmax float64 `json:"rmax"`
	// BR is the allometric exponent for respiration: W^(BR-1).
	BR float64 `json:"br"`
	// ACT is the default activity multiplier (swimming activity).
	ACT float64 `json:"act"`
	// Q10 is the temperature coefficient for respiration.
	Q10 float64 `json:"q10"`
	// TRefR is the reference temperature for respiration (°C).
	TRefR float64 `json:"t_ref_r"`

	// ===================================================================
	// Egestion and excretion parameters
	// ===================================================================

	// FA is the egestion fraction (proportion of consumed energy lost
	// as feces). Typical range: 0.10–0.20.
	FA float64 `json:"fa"`
	// UA is the excretion fraction (proportion of assimilated energy
	// lost as nitrogenous waste). Typical range: 0.05–0.10.
	UA float64 `json:"ua"`

	// ===================================================================
	// Energy density parameters
	// ===================================================================

	// EDPrey is the energy density of prey/feed (J/g wet weight).
	EDPrey float64 `json:"ed_prey"`
	// EDFish is the energy density of fish tissue (J/g wet weight).
	EDFish float64 `json:"ed_fish"`
}

// ============================================================================
// Species library
// ============================================================================

// SpeciesLibrary holds the complete species parameter library loaded
// from the embedded CSV file. It is safe for concurrent use.
type SpeciesLibrary struct {
	mu     sync.RWMutex
	params map[string]*SpeciesParams
}

// LoadSpeciesLibrary parses the embedded species CSV data and returns
// a ready-to-use SpeciesLibrary. Returns an error if the CSV is
// malformed or incomplete.
func LoadSpeciesLibrary() (*SpeciesLibrary, error) {
	reader := csv.NewReader(strings.NewReader(string(speciesCSVData)))
	reader.TrimLeadingSpace = true
	reader.Comment = '#'

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("growth: failed to read species CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("growth: species CSV must have a header plus at least one data row")
	}

	// Parse header to find column indices.
	header := records[0]
	colIdx := make(map[string]int)
	for i, col := range header {
		colIdx[strings.TrimSpace(strings.ToLower(col))] = i
	}

	lib := &SpeciesLibrary{
		params: make(map[string]*SpeciesParams, len(records)-1),
	}

	for rowIdx, row := range records[1:] {
		// Skip empty rows.
		if len(row) == 0 || (len(row) == 1 && strings.TrimSpace(row[0]) == "") {
			continue
		}

		p, err := parseSpeciesRow(row, colIdx)
		if err != nil {
			return nil, fmt.Errorf("growth: row %d: %w", rowIdx+2, err)
		}

		speciesKey := strings.ToLower(strings.TrimSpace(p.Species))
		if speciesKey == "" {
			continue
		}
		lib.params[speciesKey] = p
	}

	if len(lib.params) < 100 {
		return nil, fmt.Errorf("growth: species library has %d entries, expected 105+", len(lib.params))
	}

	return lib, nil
}

// GetParams returns the parameters for the given species. Species name
// matching is case-insensitive. Returns ErrUnsupportedSpecies if the
// species is not found.
func (lib *SpeciesLibrary) GetParams(species string) (*SpeciesParams, error) {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	key := strings.ToLower(strings.TrimSpace(species))
	p, ok := lib.params[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedSpecies, species)
	}
	return p, nil
}

// NumSpecies returns the number of species in the library.
func (lib *SpeciesLibrary) NumSpecies() int {
	lib.mu.RLock()
	defer lib.mu.RUnlock()
	return len(lib.params)
}

// SpeciesNames returns all species names in the library.
func (lib *SpeciesLibrary) SpeciesNames() []string {
	lib.mu.RLock()
	defer lib.mu.RUnlock()

	names := make([]string, 0, len(lib.params))
	for _, p := range lib.params {
		names = append(names, p.Species)
	}
	return names
}

// ============================================================================
// CSV parsing helpers
// ============================================================================

// requiredColumns lists the columns that must be present in the CSV.
var requiredColumns = []string{
	"species", "linf_cm", "k", "t0", "a", "b",
	"cmax", "bc", "t_opt", "t_max_c", "ck1", "ck2",
	"rmax", "br", "act", "q10", "t_ref_r",
	"fa", "ua", "ed_prey", "ed_fish",
}

func parseSpeciesRow(row []string, colIdx map[string]int) (*SpeciesParams, error) {
	// Verify all required columns exist.
	for _, col := range requiredColumns {
		if _, ok := colIdx[col]; !ok {
			return nil, fmt.Errorf("missing required column: %s", col)
		}
	}

	p := &SpeciesParams{}

	getField := func(col string) string {
		idx, ok := colIdx[col]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	p.Species = getField("species")
	p.CommonName = getField("common_name")

	var err error

	// VBGM parameters.
	p.Linf, err = parseFloat(getField("linf_cm"), "linf_cm")
	if err != nil {
		return nil, err
	}
	p.K, err = parseFloat(getField("k"), "k")
	if err != nil {
		return nil, err
	}
	p.T0, err = parseFloat(getField("t0"), "t0")
	if err != nil {
		return nil, err
	}
	p.A, err = parseFloat(getField("a"), "a")
	if err != nil {
		return nil, err
	}
	p.B, err = parseFloat(getField("b"), "b")
	if err != nil {
		return nil, err
	}

	// Consumption parameters.
	p.Cmax, err = parseFloat(getField("cmax"), "cmax")
	if err != nil {
		return nil, err
	}
	p.BC, err = parseFloat(getField("bc"), "bc")
	if err != nil {
		return nil, err
	}
	p.TOpt, err = parseFloat(getField("t_opt"), "t_opt")
	if err != nil {
		return nil, err
	}
	p.TMaxC, err = parseFloat(getField("t_max_c"), "t_max_c")
	if err != nil {
		return nil, err
	}
	p.CK1, err = parseFloat(getField("ck1"), "ck1")
	if err != nil {
		return nil, err
	}
	p.CK2, err = parseFloat(getField("ck2"), "ck2")
	if err != nil {
		return nil, err
	}

	// Respiration parameters.
	p.Rmax, err = parseFloat(getField("rmax"), "rmax")
	if err != nil {
		return nil, err
	}
	p.BR, err = parseFloat(getField("br"), "br")
	if err != nil {
		return nil, err
	}
	p.ACT, err = parseFloat(getField("act"), "act")
	if err != nil {
		return nil, err
	}
	p.Q10, err = parseFloat(getField("q10"), "q10")
	if err != nil {
		return nil, err
	}
	p.TRefR, err = parseFloat(getField("t_ref_r"), "t_ref_r")
	if err != nil {
		return nil, err
	}

	// Egestion and excretion.
	p.FA, err = parseFloat(getField("fa"), "fa")
	if err != nil {
		return nil, err
	}
	p.UA, err = parseFloat(getField("ua"), "ua")
	if err != nil {
		return nil, err
	}

	// Energy density.
	p.EDPrey, err = parseFloat(getField("ed_prey"), "ed_prey")
	if err != nil {
		return nil, err
	}
	p.EDFish, err = parseFloat(getField("ed_fish"), "ed_fish")
	if err != nil {
		return nil, err
	}

	return p, nil
}

// parseFloat parses a CSV field as float64, returning an error with the
// column name on failure.
func parseFloat(s, colName string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil // empty fields default to 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float for %s: %q", colName, s)
	}
	return v, nil
}
