"""
Aquaculture Feeding Environment for DDPG Reinforcement Learning.

This is an OFFLINE training environment — NEVER deployed to production.
The training produces an ONNX model consumed by the Go inference engine
(pkg/cloud/rl/).

State space (5 dimensions):
  [0] DO (dissolved oxygen, mg/L)           — range [0, 20]
  [1] Temp (water temperature, °C)          — range [0, 50]
  [2] NH3 (ammonia nitrogen, mg/L)          — range [0, 10]
  [3] FishWeight (average fish weight, g)   — range [0, 1e6]
  [4] FCR (feed conversion ratio)           — range [0.1, 10.0]

Action space (1 dimension):
  [0] feeding_rate  — continuous [0, 1] (0 = no feeding, 1 = maximum)

Reward (multi-objective):
  R = 0.4 * FCR_improvement + 0.3 * water_stability + 0.3 * energy_reduction

Water quality dynamics:
  - DO responds to aeration (decay + re-aeration) and fish respiration
  - Temperature follows a seasonal pattern with daily variation
  - NH3 accumulates with feeding (uneaten feed → ammonia) and decays naturally
  - FishWeight grows via simplified bioenergetic model
  - FCR updates based on feed consumed vs weight gained

Usage:
    import gymnasium as gym
    from feeding_env import FeedingEnv

    env = FeedingEnv()
    obs, info = env.reset()
    action = env.action_space.sample()  # or from DDPG policy
    obs, reward, terminated, truncated, info = env.step(action)
"""

import math
from typing import Optional, Tuple, Dict, Any, SupportsFloat

import numpy as np
import gymnasium as gym
from gymnasium import spaces


# ---------------------------------------------------------------------------
# Multi-objective reward weights (MUST match Go pkg/cloud/rl/rl.go)
# ---------------------------------------------------------------------------
FCR_WEIGHT = 0.4
WATER_WEIGHT = 0.3
ENERGY_WEIGHT = 0.3


def compute_reward(
    fcr_improvement: float,
    water_stability: float,
    energy_reduction: float,
) -> float:
    """Multi-objective reward: FCR×0.4 + water×0.3 + energy×0.3.

    This MUST produce identical results to Go's rl.ComputeReward().
    """
    return FCR_WEIGHT * fcr_improvement + WATER_WEIGHT * water_stability + ENERGY_WEIGHT * energy_reduction


# ---------------------------------------------------------------------------
# Default environment parameters
# ---------------------------------------------------------------------------
DEFAULT_CONFIG = {
    # Initial state
    "init_do": 8.0,
    "init_temp": 25.0,
    "init_nh3": 0.1,
    "init_fish_weight": 500.0,
    "init_fcr": 1.5,

    # DO dynamics
    "do_decay_rate": 0.1,         # Natural DO decay per step (respiration)
    "do_aeration_rate": 1.5,      # Re-aeration efficiency (from aerator)
    "do_aeration_threshold": 4.5,  # Aerator kicks in below this (mg/L)
    "do_noise_std": 0.1,          # Random environmental noise

    # Temperature dynamics
    "temp_seasonal_amplitude": 8.0,  # Seasonal variation amplitude (°C)
    "temp_daily_amplitude": 1.5,     # Daily variation amplitude (°C)
    "temp_base": 25.0,               # Mean annual temperature
    "temp_period": 365,              # Seasonal period (steps, 1 step = 1 hour → 365 days)
    "temp_noise_std": 0.2,

    # NH3 dynamics
    "nh3_feeding_factor": 0.02,   # NH3 produced per unit feeding_rate per step
    "nh3_decay_rate": 0.05,       # Natural NH3 decay (nitrification)
    "nh3_noise_std": 0.01,

    # Fish growth (simplified bioenergetic)
    "growth_base_rate": 0.5,       # Base growth rate (g/hour per kg fish)
    "growth_temp_optimum": 26.0,   # Optimal growth temperature
    "growth_temp_sensitivity": 0.05,  # Growth reduction per °C from optimum
    "growth_feed_efficiency": 0.6, # Fraction of feed converted to biomass
    "growth_noise_std": 0.1,

    # FCR dynamics
    "fcr_baseline": 1.5,
    "fcr_feed_sensitivity": 0.5,  # FCR change per unit feeding_rate above maintenance
    "fcr_growth_sensitivity": -0.3,  # FCR improvement from growth
    "fcr_noise_std": 0.02,

    # Episode
    "max_steps": 720,  # 30 days × 24h
}

# State bounds (inclusive), MUST match Go's rl.StateMin/StateMax
STATE_MIN = np.array([0.0, 0.0, 0.0, 0.0, 0.1], dtype=np.float32)
STATE_MAX = np.array([20.0, 50.0, 10.0, 1e6, 10.0], dtype=np.float32)


class FeedingEnv(gym.Env):
    """Gymnasium environment for aquaculture feeding optimization.

    The environment simulates one pond over a multi-day period with
    hourly steps. The agent controls feeding_rate ∈ [0,1] to maximize
    the multi-objective reward (FCR improvement + water stability + energy).

    This environment is designed for offline DDPG training. The trained
    policy is exported to ONNX and loaded by Go's pkg/cloud/rl/ for
    production inference — zero Python dependency in production.
    """

    # ------------------------------------------------------------------
    # Gym metadata
    # ------------------------------------------------------------------
    metadata = {"render_modes": []}

    def __init__(self, config: Optional[Dict[str, Any]] = None, render_mode: Optional[str] = None):
        super().__init__()

        self.cfg = {**DEFAULT_CONFIG, **(config or {})}
        self.render_mode = render_mode

        # Observation space: [DO, Temp, NH3, FishWeight, FCR]
        self.observation_space = spaces.Box(
            low=STATE_MIN,
            high=STATE_MAX,
            dtype=np.float32,
        )

        # Action space: feeding_rate ∈ [0, 1]
        self.action_space = spaces.Box(
            low=np.array([0.0], dtype=np.float32),
            high=np.array([1.0], dtype=np.float32),
            dtype=np.float32,
        )

        # Internal state
        self._state: np.ndarray = np.zeros(5, dtype=np.float32)
        self._step_count: int = 0
        self._prev_fcr: float = 0.0
        self._prev_nh3: float = 0.0
        self._prev_do: float = 0.0
        self._total_feed: float = 0.0
        self._total_growth: float = 0.0
        self._rng: np.random.Generator = np.random.default_rng()

    # ------------------------------------------------------------------
    # Gym API
    # ------------------------------------------------------------------

    def reset(
        self,
        seed: Optional[int] = None,
        options: Optional[Dict[str, Any]] = None,
    ) -> Tuple[np.ndarray, Dict[str, Any]]:
        """Reset the environment to initial state."""
        super().reset(seed=seed)
        if seed is not None:
            self._rng = np.random.default_rng(seed)

        self._step_count = 0

        self._state = np.array([
            self.cfg["init_do"],
            self.cfg["init_temp"],
            self.cfg["init_nh3"],
            self.cfg["init_fish_weight"],
            self.cfg["init_fcr"],
        ], dtype=np.float32)

        self._prev_fcr = float(self._state[4])
        self._prev_nh3 = float(self._state[2])
        self._prev_do = float(self._state[0])
        self._total_feed = 0.0
        self._total_growth = 0.0

        return self._state.copy(), {}

    def step(
        self, action: np.ndarray
    ) -> Tuple[np.ndarray, SupportsFloat, bool, bool, Dict[str, Any]]:
        """Execute one environment step (1 hour)."""
        feeding_rate = float(np.clip(action[0], 0.0, 1.0))
        cfg = self.cfg
        t = self._step_count  # current hour

        # Unpack current state
        do = float(self._state[0])
        temp = float(self._state[1])
        nh3 = float(self._state[2])
        fish_weight = float(self._state[3])
        fcr = float(self._state[4])

        # ------ DO dynamics ------
        # Respiration by fish + natural decay
        do_decay = cfg["do_decay_rate"] * (1.0 + 0.5 * feeding_rate)
        # Aeration: kicks in when DO drops below threshold (simulating aerator)
        aeration = 0.0
        aeration_on = do < cfg["do_aeration_threshold"]
        if aeration_on:
            aeration = cfg["do_aeration_rate"] * (cfg["do_aeration_threshold"] - do) / cfg["do_aeration_threshold"]
        do_noise = self._rng.normal(0, cfg["do_noise_std"])
        new_do = do - do_decay + aeration + do_noise
        new_do = float(np.clip(new_do, STATE_MIN[0], STATE_MAX[0]))

        # ------ Temperature dynamics ------
        # Seasonal + daily sinusoidal variation
        seasonal = cfg["temp_seasonal_amplitude"] * math.sin(2 * math.pi * t / (cfg["temp_period"]))
        daily = cfg["temp_daily_amplitude"] * math.sin(2 * math.pi * (t % 24) / 24)
        temp_noise = self._rng.normal(0, cfg["temp_noise_std"])
        new_temp = cfg["temp_base"] + seasonal + daily + temp_noise
        new_temp = float(np.clip(new_temp, STATE_MIN[1], STATE_MAX[1]))

        # ------ NH3 dynamics ------
        # Accumulates with feeding (uneaten feed → ammonia)
        nh3_input = cfg["nh3_feeding_factor"] * feeding_rate
        nh3_decay = cfg["nh3_decay_rate"] * nh3
        nh3_noise = self._rng.normal(0, cfg["nh3_noise_std"])
        new_nh3 = nh3 + nh3_input - nh3_decay + nh3_noise
        new_nh3 = float(np.clip(new_nh3, STATE_MIN[2], STATE_MAX[2]))

        # ------ Fish growth (simplified bioenergetic) ------
        # Growth rate = base × feed_efficiency × temperature_factor × feeding_rate
        temp_diff = abs(new_temp - cfg["growth_temp_optimum"])
        temp_factor = max(0.1, 1.0 - cfg["growth_temp_sensitivity"] * temp_diff)
        consumed_feed = feeding_rate * fish_weight * 0.01  # ~1% body weight per hour max
        growth = consumed_feed * cfg["growth_feed_efficiency"] * temp_factor
        growth_noise = self._rng.normal(0, cfg["growth_noise_std"])
        new_weight = fish_weight + growth + growth_noise
        new_weight = float(max(0.0, new_weight))

        # Maintenance respiration (fish loses weight if no feed)
        if feeding_rate < 0.02:
            maintenance_loss = fish_weight * 0.0005  # 0.05% per hour
            new_weight = max(0.0, new_weight - maintenance_loss)

        # ------ FCR dynamics ------
        # FCR = feed_consumed / weight_gained (lower is better)
        # FCR improves with efficient feeding and degrades with overfeeding
        weight_gain = max(0.001, new_weight - fish_weight)
        effective_fcr = consumed_feed / weight_gain if weight_gain > 0 else fcr
        fcr_noise = self._rng.normal(0, cfg["fcr_noise_std"])
        # Exponential moving average towards effective FCR
        new_fcr = fcr * 0.95 + effective_fcr * 0.05 + fcr_noise
        new_fcr = float(np.clip(new_fcr, STATE_MIN[4], STATE_MAX[4]))

        # ------ Update state ------
        self._state[0] = new_do
        self._state[1] = new_temp
        self._state[2] = new_nh3
        self._state[3] = new_weight
        self._state[4] = new_fcr

        # ------ Reward ------
        # FCR improvement: lower FCR (more efficient) = higher reward
        fcr_improvement = max(0.0, (self._prev_fcr - new_fcr) / max(0.5, self._prev_fcr))

        # Water quality stability: how close to ideal ranges
        # Ideal DO: 6-8, ideal NH3: <0.5, ideal temp: 22-28
        do_stability = 1.0 - abs(new_do - 7.0) / 7.0  # 1 at DO=7, 0 at DO=0 or 14
        nh3_stability = 1.0 - new_nh3 / 2.0  # 1 at NH3=0, 0 at NH3=2
        temp_stability = 1.0 - abs(new_temp - 25.0) / 25.0  # 1 at 25°C
        water_stability = float(np.clip((do_stability + nh3_stability + temp_stability) / 3.0, 0.0, 1.0))

        # Energy reduction: moderate feeding saves energy (aerator + feeder motor)
        # Aeration energy: high when aeration is on
        aeration_energy = 1.0 if aeration_on else 0.0
        # Feeding energy: proportional to feeding_rate
        feeding_energy = feeding_rate
        total_energy = 0.7 * aeration_energy + 0.3 * feeding_energy
        energy_reduction = 1.0 - total_energy  # 1 when no energy used

        reward = compute_reward(fcr_improvement, water_stability, energy_reduction)

        # ------ Track state for next reward ------
        self._prev_fcr = new_fcr
        self._prev_nh3 = new_nh3
        self._prev_do = new_do
        self._total_feed += consumed_feed
        self._total_growth += weight_gain
        self._step_count += 1

        # ------ Episode termination ------
        terminated = False
        truncated = self._step_count >= cfg["max_steps"]
        # Safety termination: crash if DO drops critically low
        if new_do < 2.0:
            terminated = True
        # Crash if NH3 spikes critically high
        if new_nh3 > 5.0:
            terminated = True

        info = {
            "step": self._step_count,
            "feeding_rate": feeding_rate,
            "fcr_improvement": fcr_improvement,
            "water_stability": water_stability,
            "energy_reduction": energy_reduction,
            "aeration_on": aeration_on,
            "growth": growth,
            "weight_gain": weight_gain,
            "consumed_feed": consumed_feed,
        }

        return self._state.copy(), reward, terminated, truncated, info

    # ------------------------------------------------------------------
    # Utility
    # ------------------------------------------------------------------

    def seed(self, seed: Optional[int] = None) -> list:
        """Set the random seed for reproducibility."""
        self._rng = np.random.default_rng(seed)
        return [seed]

    def close(self) -> None:
        """Clean up environment resources."""
        pass


# ---------------------------------------------------------------------------
# Registration helper
# ---------------------------------------------------------------------------
def register_env():
    """Register the FeedingEnv with Gymnasium for use with SB3."""
    try:
        gym.register(
            id="FeedingEnv-v0",
            entry_point="feeding_env:FeedingEnv",
            max_episode_steps=DEFAULT_CONFIG["max_steps"],
        )
    except gym.error.Error:
        pass  # Already registered
