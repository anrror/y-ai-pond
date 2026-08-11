package twin

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// ============================================================================
// HydroDynamicsEngine — simplified Navier-Stokes solver
// ============================================================================

// HydroDynamicsEngine implements SimulationEngine using a simplified 2D
// shallow-water formulation with explicit forward-Euler time-stepping and
// central-difference spatial discretization on a uniform Cartesian grid.
//
// ============================================================================
// Governing equations (simplified from full Navier-Stokes):
//
// 1. MOMENTUM (depth-averaged, incompressible):
//
//	∂u/∂t = -u·∂u/∂x - v·∂u/∂y + ν·(∂²u/∂x² + ∂²u/∂y²) + f_wind_x
//	∂v/∂t = -u·∂v/∂x - v·∂v/∂y + ν·(∂²v/∂x² + ∂²v/∂y²) + f_wind_y
//
//	where ν is the turbulent eddy viscosity (m²/s) and f_wind is wind shear.
//
// 2. SCALAR TRANSPORT (temperature, DO, NH₃, turbidity):
//
//	∂C/∂t = -u·∂C/∂x - v·∂C/∂y + D·(∂²C/∂x² + ∂²C/∂y²) + S_C
//
//	where D is the turbulent diffusivity for scalar C and S_C is the
//	source/sink term (solar heating, reaeration, consumption, settling).
//
// ============================================================================
// Terms retained / simplified:
//
//	RETAINED:
//	  - Advection (nonlinear): (V·∇)V and V·∇C
//	  - Turbulent diffusion: ν∇²V and D∇²C
//	  - Wind shear forcing at the surface
//	  - Solar radiative heating
//	  - DO reaeration and consumption
//	  - NH₃ accumulation / decay
//	  - Turbidity advection-diffusion with settling
//
//	OMITTED (beyond v1 skeleton scope):
//	  - Coriolis force (negligible at pond scale, Rossby number >> 1)
//	  - Baroclinic pressure gradients (uniform density assumption)
//	  - Bottom friction (simplified into eddy viscosity)
//	  - Full 3D vertical structure (2D depth-averaged only)
//	  - Wetting/drying (fixed water depth)
//	  - Precipitation / evaporation mass flux
//
// ============================================================================
// Numerical method:
//
//   - Time:  explicit forward Euler (O(dt)).
//
//   - Space: central differences on a uniform grid (O(dx²)).
//
//     CFL stability condition:
//     CFL_adv = max(|u|,|v|)·dt / dx ≤ 1
//     CFL_diff = max(ν,D)·dt / dx² ≤ 0.5
//
//     For default config (dx≈12m, dt=300s, ν≈0.05, D≈0.1):
//     CFL_adv ≈ 0.03·300/12 = 0.75  → stable
//     CFL_diff ≈ 0.1·300/144 = 0.21 → stable
//
// ============================================================================
// Boundary conditions:
//
//	All boundaries use zero-gradient (Neumann): ∂C/∂n = 0, ∂V/∂n = 0.
//	For inlet flow, the left edge (x=0) has prescribed velocity u_inlet
//	computed from InletFlow / (WaterDepth × pond_width).
type HydroDynamicsEngine struct {
	// EddyViscosity is the turbulent eddy viscosity ν (m²/s). Default: 0.05.
	EddyViscosity float64

	// ThermalDiffusivity is the turbulent thermal diffusivity (m²/s). Default: 0.1.
	ThermalDiffusivity float64

	// DODiffusivity is the turbulent DO diffusivity (m²/s). Default: 0.05.
	DODiffusivity float64

	// NH3Diffusivity is the turbulent NH₃ diffusivity (m²/s). Default: 0.05.
	NH3Diffusivity float64

	// TurbidityDiffusivity is the turbulent turbidity diffusivity (m²/s). Default: 0.01.
	TurbidityDiffusivity float64

	// WindDragCoefficient is the surface wind drag coefficient C_d. Default: 0.0015.
	WindDragCoefficient float64

	// ReaerationRate is the DO reaeration coefficient k_r (1/s). Default: 1e-7.
	ReaerationRate float64

	// ReaerationWindFactor scales reaeration with wind speed squared.
	// Effective k_r = ReaerationRate + ReaerationWindFactor · W².
	// Default: 2e-8.
	ReaerationWindFactor float64

	// DOConsumptionRate is the biological DO consumption coefficient k_c (1/s).
	// Default: 2.5e-6.
	DOConsumptionRate float64

	// NH3DecayRate is the NH₃ natural decay/nitrification rate (1/s). Default: 5e-6.
	NH3DecayRate float64

	// TurbiditySettlingRate is the particle settling velocity (m/s). Default: 1e-5.
	TurbiditySettlingRate float64
}

// defaultEngine returns a HydroDynamicsEngine with physically reasonable defaults
// calibrated for a typical aquaculture pond (O(100 m²), O(2 m depth)).
func defaultEngine() *HydroDynamicsEngine {
	return &HydroDynamicsEngine{
		EddyViscosity:         0.05,
		ThermalDiffusivity:    0.1,
		DODiffusivity:         0.05,
		NH3Diffusivity:        0.05,
		TurbidityDiffusivity:  0.01,
		WindDragCoefficient:   0.0015,
		ReaerationRate:        1e-7,
		ReaerationWindFactor:  2e-8,
		DOConsumptionRate:     2.5e-6,
		NH3DecayRate:          5e-6,
		TurbiditySettlingRate: 1e-5,
	}
}

// Name returns the engine identifier.
func (e *HydroDynamicsEngine) Name() string {
	return "HydroDynamicsEngine"
}

// Simulate runs the simplified Navier-Stokes simulation.
func (e *HydroDynamicsEngine) Simulate(ctx context.Context, cfg SimulationConfig) (*SimulationResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}

	eng := e
	if eng == nil {
		eng = defaultEngine()
	}

	n := cfg.GridSize
	dx := cfg.GridSpacing()               // m
	dt := float64(cfg.StepMinutes) * 60.0 // seconds
	nt := cfg.TimeSteps

	// Precompute physics constants.
	depth := cfg.WaterDepth    // m
	wind := cfg.WindSpeed      // m/s
	solar := cfg.SolarFlux     // W/m²
	inletTemp := cfg.InletTemp // °C
	inletFlow := cfg.InletFlow // m³/s

	// Water properties.
	rhoWater := 1000.0 // kg/m³
	rhoAir := 1.225    // kg/m³
	cpWater := 4184.0  // J/(kg·K)

	// Inlet velocity: U_in = Q / (h · W_pond), W_pond = n * dx (m).
	pondWidth := float64(n) * dx // m (assume square pond)
	inletVelocity := 0.0
	if inletFlow > 0 && depth > 0 && pondWidth > 0 {
		inletVelocity = inletFlow / (depth * pondWidth)
	}

	// Allocate 3D arrays: [timeStep][x][y].
	u := alloc3D(nt, n)
	v := alloc3D(nt, n)
	temp := alloc3D(nt, n)
	do := alloc3D(nt, n)
	nh3 := alloc3D(nt, n)
	turb := alloc3D(nt, n)

	// ── Initial conditions ───────────────────────────────────────────
	initTemp := 20.0     // °C, uniform
	initDO := 8.0        // mg/L, saturated at 20°C
	initNH3 := 0.1       // mg/L, baseline
	initTurbidity := 5.0 // NTU, baseline
	initU := 0.0         // m/s
	initV := 0.0         // m/s

	for x := range n {
		for y := range n {
			u[0][x][y] = initU
			v[0][x][y] = initV
			temp[0][x][y] = initTemp
			do[0][x][y] = initDO
			nh3[0][x][y] = initNH3
			turb[0][x][y] = initTurbidity
		}
	}

	// Precompute wind magnitude for forcing terms.
	absWind := wind
	if wind < 0 {
		absWind = -wind
	}

	// ── Time-stepping loop (forward Euler) ───────────────────────────
	for step := 0; step < nt-1; step++ {
		// Check context cancellation every time step.
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("hydrodynamics simulate: context cancelled at step %d: %w", step, ctx.Err())
		default:
		}

		next := step + 1
		for x := range n {
			for y := range n {
				// ── Momentum: u-velocity ───────────────────────────
				advUx := advectionTerm(u[step], u[step], x, y, dx, n)
				advUy := advectionTerm(v[step], u[step], x, y, dx, n)
				diffU := laplacian(u[step], x, y, dx, n)

				// Wind shear: τ / (ρ·h) = ρ_air·C_d·W·|W| / (ρ_water·h).
				// Use wind speed magnitude; direction is always positive-x for simplicity.
				windShear := rhoAir * eng.WindDragCoefficient * absWind * wind / (rhoWater * depth)
				if depth < 0.01 {
					windShear = 0
				}

				u[next][x][y] = u[step][x][y] + dt*(-advUx-advUy+
					eng.EddyViscosity*diffU+
					windShear)

				// ── Momentum: v-velocity ───────────────────────────
				advVx := advectionTerm(u[step], v[step], x, y, dx, n)
				advVy := advectionTerm(v[step], v[step], x, y, dx, n)
				diffV := laplacian(v[step], x, y, dx, n)

				v[next][x][y] = v[step][x][y] + dt*(-advVx-advVy+
					eng.EddyViscosity*diffV)

				// ── Temperature ─────────────────────────────────────
				advTempX := advectionTerm(u[step], temp[step], x, y, dx, n)
				advTempY := advectionTerm(v[step], temp[step], x, y, dx, n)
				diffTemp := laplacian(temp[step], x, y, dx, n)

				// Solar heating: dT/dt = solar · (1-albedo) / (ρ·cp·h).
				// Albedo ≈ 0.06 for water; only at surface layer.
				solarHeating := 0.0
				if depth > 0.01 {
					solarHeating = solar * (1.0 - 0.06) / (rhoWater * cpWater * depth)
				}

				temp[next][x][y] = temp[step][x][y] + dt*(-advTempX-advTempY+
					eng.ThermalDiffusivity*diffTemp+
					solarHeating)

				// ── Dissolved Oxygen ─────────────────────────────────
				advDOx := advectionTerm(u[step], do[step], x, y, dx, n)
				advDOy := advectionTerm(v[step], do[step], x, y, dx, n)
				diffDO := laplacian(do[step], x, y, dx, n)

				// DO saturation (simplified empirical fit).
				doSat := doSaturation(temp[step][x][y])
				// Reaeration: effective k_r = base + wind-dependent.
				// k_r_base handles natural surface exchange; wind enhances it.
				krEff := eng.ReaerationRate + eng.ReaerationWindFactor*absWind*absWind
				reaeration := krEff * (doSat - do[step][x][y])
				// Biological consumption: k_c · DO · f(T).
				// Use temperature-adjusted rate (stronger in warm water).
				tempFactor := temperatureFactor(temp[step][x][y])
				consumption := eng.DOConsumptionRate * do[step][x][y] * tempFactor

				do[next][x][y] = do[step][x][y] + dt*(-advDOx-advDOy+
					eng.DODiffusivity*diffDO+
					reaeration-
					consumption)

				// ── NH₃ ─────────────────────────────────────────────
				advNH3x := advectionTerm(u[step], nh3[step], x, y, dx, n)
				advNH3y := advectionTerm(v[step], nh3[step], x, y, dx, n)
				diffNH3 := laplacian(nh3[step], x, y, dx, n)

				// NH₃ decay (nitrification): -k_n · NH₃ · f(T).
				decay := eng.NH3DecayRate * nh3[step][x][y] * tempFactor

				nh3[next][x][y] = nh3[step][x][y] + dt*(-advNH3x-advNH3y+
					eng.NH3Diffusivity*diffNH3-
					decay)

				// ── Turbidity ────────────────────────────────────────
				advTurbX := advectionTerm(u[step], turb[step], x, y, dx, n)
				advTurbY := advectionTerm(v[step], turb[step], x, y, dx, n)
				diffTurb := laplacian(turb[step], x, y, dx, n)

				// Settling: -v_settle · C / h (depth-averaged).
				setting := 0.0
				if depth > 0.01 {
					setting = eng.TurbiditySettlingRate * turb[step][x][y] / depth
				}

				turb[next][x][y] = turb[step][x][y] + dt*(-advTurbX-advTurbY+
					eng.TurbidityDiffusivity*diffTurb-
					setting)

				// ── Clamp to physical bounds ─────────────────────────
				if temp[next][x][y] < -10 {
					temp[next][x][y] = -10
				}
				if do[next][x][y] < 0 {
					do[next][x][y] = 0
				}
				if nh3[next][x][y] < 0 {
					nh3[next][x][y] = 0
				}
				if turb[next][x][y] < 0 {
					turb[next][x][y] = 0
				}
			}
		}

		// ── Inlet boundary condition (Dirichlet) ───────────────────
		// Apply after physics update. The left edge (x=0) has prescribed
		// inflow velocity and temperature. This replaces the physics
		// update at the boundary with a fixed Dirichlet condition.
		if inletFlow > 0 && inletVelocity > 0 {
			for y := range n {
				u[next][0][y] = inletVelocity
				v[next][0][y] = 0
				temp[next][0][y] = inletTemp
			}
		}

		// ── Clamp velocity magnitude for CFL safety ─────────────────
		maxV := 0.0
		for x := range n {
			for y := range n {
				vm := u[next][x][y]*u[next][x][y] + v[next][x][y]*v[next][x][y]
				if vm > maxV {
					maxV = vm
				}
			}
		}
		maxV = math.Sqrt(maxV)
		// If velocity exceeds CFL limit, clamp globally.
		cflLimit := 0.5 * dx / dt
		if maxV > cflLimit {
			scale := cflLimit / maxV
			for x := range n {
				for y := range n {
					u[next][x][y] *= scale
					v[next][x][y] *= scale
				}
			}
		}
	}

	// ── Validate: check for NaN/Inf ──────────────────────────────────
	if err := validateVolume(u, "FlowVx"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}
	if err := validateVolume(v, "FlowVy"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}
	if err := validateVolume(temp, "WaterTemp"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}
	if err := validateVolume(do, "DOConc"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}
	if err := validateVolume(nh3, "NH3Conc"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}
	if err := validateVolume(turb, "Turbidity"); err != nil {
		return nil, fmt.Errorf("hydrodynamics simulate: %w", err)
	}

	return &SimulationResult{
		GridSize:  n,
		TimeSteps: nt,
		WaterTemp: temp,
		FlowVx:    u,
		FlowVy:    v,
		DOConc:    do,
		NH3Conc:   nh3,
		Turbidity: turb,
	}, nil
}

// ============================================================================
// Numerical helpers (finite differences)
// ============================================================================

// advectionTerm computes V_x · ∂C/∂x or V_y · ∂C/∂y at grid point (x,y).
// velocityField provides the advecting velocity component,
// scalarField is the transported quantity.
func advectionTerm(velocityField [][]float64, scalarField [][]float64, x, y int, dx float64, n int) float64 {
	vLocal := velocityField[x][y]
	// Central difference for gradient.
	grad := centralDiff(scalarField, x, y, dx, n)
	return vLocal * grad
}

// laplacian computes ∂²C/∂x² + ∂²C/∂y² at grid point (x,y) using
// the 3-point central finite difference stencil: (f_{i+1} - 2f_i + f_{i-1})/dx².
func laplacian(field [][]float64, x, y int, dx float64, n int) float64 {
	dx2 := dx * dx
	// x-direction.
	d2x := (field[idx(x+1, n)][y] - 2*field[x][y] + field[idx(x-1, n)][y]) / dx2
	// y-direction.
	d2y := (field[x][idx(y+1, n)] - 2*field[x][y] + field[x][idx(y-1, n)]) / dx2
	return d2x + d2y
}

// centralDiff computes ∂C/∂x or ∂C/∂y using central difference:
// (C_{i+1} - C_{i-1}) / (2·dx).
func centralDiff(field [][]float64, x, y int, dx float64, n int) float64 {
	return (field[idx(x+1, n)][y] - field[idx(x-1, n)][y]) / (2 * dx)
}

// idx returns a clamped index for ghost-cell boundary handling.
// Neumann (zero-gradient) boundary: the ghost cell value equals the
// boundary cell value, which means ∂C/∂n = 0.
func idx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// ============================================================================
// Physical sub-models
// ============================================================================

// doSaturation returns the equilibrium dissolved oxygen concentration (mg/L)
// at a given water temperature (°C) and standard atmospheric pressure.
// Formula: simplified empirical fit to Benson & Krause (1984) data.
//
//	DO_sat(T) ≈ 14.652 - 0.41022·T + 0.007991·T² - 0.00007777·T³
func doSaturation(tempC float64) float64 {
	t := tempC
	return 14.652 - 0.41022*t + 0.007991*t*t - 0.00007777*t*t*t
}

// temperatureFactor returns a dimensionless multiplier for biological
// activity as a function of water temperature. Peaks near 25-30°C and
// drops at extreme temperatures.
// Simplified Q10-like model: f(T) = 2^((T-20)/10), clamped to [0, 5].
func temperatureFactor(tempC float64) float64 {
	if tempC <= 0 {
		return 0.1
	}
	factor := math.Pow(2.0, (tempC-20.0)/10.0)
	if factor > 5.0 {
		return 5.0
	}
	if factor < 0.05 {
		return 0.05
	}
	return factor
}

// ============================================================================
// Memory and validation helpers
// ============================================================================

// alloc3D allocates a [timeSteps][gridSize][gridSize] float64 volume.
func alloc3D(timeSteps, gridSize int) [][][]float64 {
	vol := make([][][]float64, timeSteps)
	for t := range timeSteps {
		vol[t] = make([][]float64, gridSize)
		for x := range gridSize {
			vol[t][x] = make([]float64, gridSize)
		}
	}
	return vol
}

// validateVolume checks a 3D field for NaN or Inf values.
func validateVolume(field [][][]float64, name string) error {
	for s := range field {
		for x := range field[s] {
			for y, val := range field[s][x] {
				if math.IsNaN(val) {
					return fmt.Errorf("field %q contains NaN at [%d][%d][%d]", name, s, x, y)
				}
				if math.IsInf(val, 0) {
					return fmt.Errorf("field %q contains Inf at [%d][%d][%d]", name, s, x, y)
				}
			}
		}
	}
	return nil
}

// ============================================================================
// Static errors
// ============================================================================

// ErrInvalidConfig is returned when the simulation configuration fails validation.
var ErrInvalidConfig = errors.New("invalid simulation configuration")

// ErrSimulationFailed is returned when the simulation produces NaN or Inf values.
var ErrSimulationFailed = errors.New("simulation produced invalid numeric values")
