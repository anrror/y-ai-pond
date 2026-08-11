# μDT Distributed Architecture & Edge Deployment Plan

> **Version**: 1.0 | **Date**: 2026-08-11 | **Task**: T30

## 1. Overview

The μDT (micro digital twin) is a lightweight digital twin instance that runs on
the edge device (RK3588 / Jetson Orin Nano) alongside the main Edge Controller.
It mirrors the cloud Digital Twin's water-body simulation but with a simplified
physics model — the same deterministic PondSimulator from `pkg/dt/scenario/`.

The μDT serves two purposes:

1. **Offline resilience**: when the MQTT link to the cloud is down, the μDT
   continues to simulate the pond's water quality locally, so that when the
   connection is restored the cloud can replay the buffered states and catch up.
2. **Edge-first visualization**: local dashboard panels can query the μDT for
   near-real-time virtual water state without a round-trip to the cloud.

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     CLOUD (TIER 2)                               │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  DigitalTwin Manager (pkg/cloud/twin/)                     │  │
│  │  ┌─────────────┐  ┌──────────────────┐                    │  │
│  │  │ DT Instance  │  │ HydroDynamics    │                    │  │
│  │  │ (per pond)   │  │ Engine           │                    │  │
│  │  └──────┬──────┘  └──────────────────┘                    │  │
│  │         │  ▲ full physics (2D N-S)                         │  │
│  │         │  │ ST-GNN inference (onnxer)                     │  │
│  │         │  │ DDPG policy search                            │  │
│  └─────────┼──┼──────────────────────────────────────────────┘  │
│            │  │                                                  │
│   ┌────────┼──┼──────────┐                                      │
│   │  State Replay Service │ ← receives BufferedState[]          │
│   │  (backfill handler)   │   replays in sequence order          │
│   └───────────────────────┘                                      │
└─────────────────────────────────────────────────────────────────┘
            │  ▲
     MQTT v5│  │ BufferedState[] payload (JSON, batch)
            │  │
┌───────────┼──┼─────────────────────────────────────────────────┐
│           ▼  │                  EDGE (TIER 1)                    │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  Edge Controller (cmd/edge/)                              │  │
│  │  ┌───────────────┐   ┌──────────────────────────────┐    │  │
│  │  │ Main Loop     │   │ LocalDTMicro (μDT)           │    │  │
│  │  │ (100 Hz)      │   │                              │    │  │
│  │  │               │   │  Step()  → WaterState        │    │  │
│  │  │ YOLOv8n       │   │  State() → WaterState        │    │  │
│  │  │ Fuzzy-PID     │   │  Sync()  → BufferedState[]   │    │  │
│  │  │ Safety        │   │                              │    │  │
│  │  │ HAL           │   │  [seq:1] → [seq:2] → ...     │    │  │
│  │  └───────┬───────┘   └──────────────┬───────────────┘    │  │
│  │          │                          │                     │  │
│  │  ┌───────┴──────────────────────────┴───────────────┐    │  │
│  │  │  MQTT Client (paho.golang/autopaho)              │    │  │
│  │  │  OnConnectionUp → trigger μDT.Sync() + publish   │    │  │
│  │  │  OnConnectionDown → μDT.SetConnected(false)      │    │  │
│  │  └──────────────────────────────────────────────────┘    │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  Hardware: RK3588 (8-core ARM, 6 TOPS NPU, 16 GB RAM)          │
└─────────────────────────────────────────────────────────────────┘
```

### 2.1 Component Roles

| Component | Location | Role |
|-----------|----------|------|
| Cloud DigitalTwin | Cloud | Full physics (2D N-S) + GNN inference + DDPG search |
| State Replay Service | Cloud | Receives BufferedState batches, replays into cloud DT |
| LocalDTMicro (μDT) | Edge | Deterministic PondSimulator; Step/State/Sync API |
| MQTT Client | Edge | Connection lifecycle → triggers Sync; publishes backfill |
| Edge Controller | Edge | Orchestrates main loop + μDT stepping |

## 3. MQTT Disconnect 72h Degradation Design

### 3.1 State Machine

```
                  ┌──────────────────────────┐
                  │      CONNECTED           │
                  │  μDT.Step() every tick   │
                  │  Sync() → publish to     │
                  │  pond/v1/+/+/dt/sync     │
                  └───────┬──────────────────┘
                          │ MQTT connection lost
                          ▼
                  ┌──────────────────────────┐
                  │      DISCONNECTED        │
                  │  μDT.Step() continues    │
                  │  States buffered locally │
                  │  Buffer ≤ 5000 entries   │
                  └───────┬──────────────────┘
                          │ MQTT connection restored
                          ▼
                  ┌──────────────────────────┐
                  │      BACKFILL            │
                  │  μDT.Sync() → batch      │
                  │  Publish to cloud        │
                  │  Cloud replays in order  │
                  └───────┬──────────────────┘
                          │ Buffer drained
                          ▼
                  ┌──────────────────────────┐
                  │      CONNECTED           │
                  │  (resume normal)         │
                  └──────────────────────────┘
```

### 3.2 Buffer Design

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Default buffer size | 5,000 entries | 72h × 60 steps/h + 10% margin = 4,752 → 5,000 |
| Entry size | ~100 bytes (water state + seq) | 4×float64 + 1×int64 + JSON overhead |
| Total buffer memory | ~500 KB | Negligible on RK3588 (16 GB) |
| Overflow policy | LRU eviction (oldest first) | Preserves recent trend; oldest data least valuable for catch-up |
| Overflow error | `ErrBufferOverflow` returned | Caller can log, alert, or force early flush |
| Sync format | JSON `BufferedState[]` | Human-readable, no protobuf dependency needed for DT sync |

### 3.3 Disconnect Scenarios

| Duration | Behavior | Cloud Impact |
|----------|----------|--------------|
| < 1h | Buffer < 60 entries. Full backfill. | DT replays all states; negligible catch-up delay. |
| 1-24h | Buffer 60-1,440 entries. Full backfill. | DT replays ~1,440 steps; < 1s of cloud compute. |
| 24-72h | Buffer 1,440-4,320 entries. Full backfill. | DT replays linearly; ~3s of cloud compute. |
| > 72h | Buffer > 5,000 → LRU eviction. Oldest data lost. | Cloud DT fast-forwards to latest state; historical gap logged. |

## 4. Data Sync Protocol

### 4.1 Topic Structure

```
pond/v1/{farm_id}/{pond_id}/dt/
├── state          ← BufferedState    (QoS 0, real-time virtual state)
├── sync/batch     ← BufferedState[]  (QoS 1, backfill on reconnect)
└── sync/ack       ← {last_seq: N}    (QoS 1, cloud acknowledges batch)
```

### 4.2 Connected Mode (Normal Operations)

1. Edge Controller main loop ticks at ~1 Hz (or configurable interval).
2. Each tick: `μDT.Step()` → publishes single `BufferedState` to `dt/state`.
3. Cloud DT consumes `dt/state` → updates virtual state in real time.
4. QoS 0 (best-effort): single state loss is tolerable; next tick overwrites.

### 4.3 Disconnect → Reconnect (Backfill)

1. MQTT client detects disconnection → `μDT.SetConnected(false)`.
2. μDT continues stepping locally; states buffer with sequence numbers.
3. MQTT client reconnects → `OnConnectionUp` fires.
4. Edge calls `μDT.Sync()` → receives `BufferedState[]` in order.
5. Edge publishes batch to `dt/sync/batch` (QoS 1).
6. Cloud State Replay Service replays each `(seq, state)` into the cloud DT in order.
7. Cloud publishes `sync/ack {last_seq}` to confirm receipt.
8. Edge resumes normal `dt/state` publishing.

### 4.4 Message Format

```json
// Single state (dt/state)
{
  "seq": 1042,
  "state": {
    "temperature_c": 26.3,
    "do_mg_l": 6.8,
    "turbidity_ntu": 13.5,
    "nh3_mg_l": 0.07
  }
}

// Batch (dt/sync/batch)
[
  {
    "seq": 1001,
    "state": {
      "temperature_c": 26.1,
      "do_mg_l": 6.9,
      "turbidity_ntu": 12.8,
      "nh3_mg_l": 0.06
    }
  },
  { "seq": 1002, "state": { ... } },
  ...
]
```

### 4.5 Idempotency

- Cloud replay is idempotent by sequence number: if `seq ≤ lastSyncedSeq`, skip.
- Edge retransmits batch if `sync/ack` is not received within a timeout (30s).
- Cloud `sync/ack` includes `last_seq` so edge knows which entries were received.

## 5. Hardware Resource Assessment

### 5.1 RK3588 Compute Budget

| Resource | Available | μDT Usage | Headroom |
|----------|-----------|-----------|----------|
| CPU cores | 8 (4×A76 @ 2.4 GHz + 4×A55 @ 1.8 GHz) | < 1% of 1×A55 core | 99%+ |
| RAM | 16 GB LPDDR4x | ~500 KB buffer + code | 99.99%+ |
| Storage | eMMC 64 GB+ | Negligible (in-memory only) | 99.99%+ |
| NPU | 6 TOPS (INT8) | Not used | 100% reserved for YOLOv8n |

### 5.2 Compute Analysis

The μDT uses the `scenario.PondSimulator` which performs four floating-point
operations per step:

- Temperature: 2×add, 2×mul (relaxation)
- DO: 3×add, 4×mul (reaeration + consumption + flood)
- Turbidity: 2×add, 2×mul (settle + flood)
- NH3: 2×add, 2×mul (accumulation)

Total: ~19 FLOP/step (single-precision equivalent).

At 1 Hz step rate: 19 FLOP/s ≈ **19 FLOPS**. An A55 core at 1.8 GHz can
theoretically issue ~3.6 GFLOPS. The μDT consumes **~0.0000005%** of one core.

At 1,000 Hz step rate (stress test): 19,000 FLOP/s ≈ **19 KFLOPS**, still
< 0.001% of one A55 core.

**Conclusion**: The μDT is not a compute bottleneck. The NPU (6 TOPS) is
fully reserved for YOLOv8n, the main compute consumer on the edge device.

### 5.3 Memory Footprint

```
micro.go  binary:      ~3 KB  (compiled Go)
buffer (5,000 entry):  ~500 KB (heap)
state + seq:             ~64 B (stack/struct)
─────────────────────────────────
Total:                 ~504 KB
```

### 5.4 Jetson Orin Nano (Backup Platform)

| Resource | Available | μDT Usage |
|----------|-----------|-----------|
| CPU | 6-core ARM A78AE @ 1.5 GHz | < 0.001% |
| RAM | 8 GB LPDDR5 | < 10 KB |
| GPU | 1024-core Ampere, 40 TOPS | Not used |

The μDT is trivially portable: no platform-specific code, stdlib-only Go.

## 6. Deployment Guide

### 6.1 Edge Device Setup

The μDT is embedded in the Edge Controller binary (`cmd/edge/`). No separate
deployment step is needed.

```go
// Example: integrating μDT into the edge controller main loop.
import "github.com/anrror/y-ai-pond/pkg/dt/micro"

func main() {
    // Initialize μDT with a baseline scenario.
    dt := micro.New(micro.Config{
        Scenario:  micro.DefaultScenario(),
        MaxBuffer: 0, // use DefaultMaxBuffer (5000)
    })

    // MQTT connection lifecycle.
    mqttClient.OnConnectionUp = func() {
        dt.SetConnected(true)
        // Backfill any buffered states from offline period.
        if batch := dt.Sync(); len(batch) > 0 {
            publish("pond/v1/{farm}/{pond}/dt/sync/batch", batch)
        }
    }
    mqttClient.OnConnectionDown = func() {
        dt.SetConnected(false)
    }

    // Main loop (1 Hz for DT, 100 Hz for control).
    ticker := time.NewTicker(1 * time.Second)
    for range ticker.C {
        state, err := dt.Step()
        if errors.Is(err, micro.ErrBufferOverflow) {
            slog.Warn("μDT buffer overflow, oldest data evicted")
        }
        if dt.IsConnected() {
            publish("pond/v1/{farm}/{pond}/dt/state", micro.BufferedState{
                Seq: /* from dt */, State: state,
            })
        }
    }
}
```

### 6.2 Cloud Side

The cloud needs a lightweight State Replay Service that:

1. Subscribes to `pond/v1/+/+/dt/sync/batch` (QoS 1).
2. On receipt, replays each `BufferedState` into the cloud DigitalTwin
   instance for that pond.
3. Publishes `pond/v1/{farm}/{pond}/dt/sync/ack` with `last_seq`.

### 6.3 Build & Deploy

```powershell
# Cross-compile for RK3588 (ARM64 Linux).
$env:GOOS="linux"; $env:GOARCH="arm64"; go build ./cmd/edge/

# Copy binary + config to edge device.
scp edge pi@rk3588:/opt/y-ai-pond/
```

No containers needed on edge (Go static binary). No ML runtime dependency
(μDT is stdlib-only).

## 7. Testing Strategy

### Unit Tests (pkg/dt/micro/)

| Test | Coverage |
|------|----------|
| TestMicroDTSync | Step N → Sync → verify N ordered states received |
| TestMicroDTOffline | Connected → disconnect → Step → reconnect → Sync → backfill |
| TestMicroDTOffline_EmptySync | Sync on empty buffer returns nil |
| TestMicroDTOffline_LRUEviction | Overflow → oldest evicted, ErrBufferOverflow |
| TestMicroDTOffline_MultipleSync | Repeated Sync cycles, no seq collision |
| TestMicroDTOffline_ConcurrentSafety | 8 goroutines × 50 Step calls, monotonic seq |
| TestMicroDTOffline_StatePreserved | State() returns consistent value across transitions |
| TestMicroDTOffline_DefaultScenario | Zero-config scenario defaults to baseline |
| TestMicroDTOffline_HeatWaveScenario | Heat wave drives temp up, DO down |
| TestMicroDTOffline_StormFloodScenario | Flood drives turbidity up, DO down |
| TestMicroDTOffline_ColdSnapScenario | Cold snap drives temp down |

### Network Partition Test (future integration)

1. Start EMQX broker (Docker or local).
2. Edge Controller with μDT connects.
3. Kill broker → verify μDT continues stepping locally.
4. Restart broker → verify backfill batch arrives at cloud.
5. Assert: all seq numbers contiguous, no gaps.

This test is described here for completeness but implemented under T31
(end-to-end integration tests).

## 8. Limitations & Future Work

| Limitation | Mitigation |
|------------|-----------|
| μDT uses simplified physics (0-D PondSimulator) vs cloud's 2D N-S solver | Acceptable: edge is for continuity, not accuracy. Cloud DT remains authoritative. |
| No GNN inference on edge | By design (per T30 "Must NOT do"). Cloud handles GNN. |
| Buffer is in-memory only (no SQLite persistence) | Acceptable for v1. If edge power-cycles during >72h offline, data loss is bounded. Future: persist buffer to SQLite (T27's local buffer already exists). |
| Single-scenario (no dynamic scenario switching) | Future: allow cloud to push new Scenario via MQTT config topic. |

## 9. References

- T26: `pkg/cloud/twin/` — cloud DigitalTwin manager + HydroDynamicsEngine
- T28: `pkg/dt/scenario/` — PondSimulator, scenario library
- T29: `pkg/dt/visual/` — visualization data API
- Plan spec: `.omo/plans/y-ai-pond.md` lines 689-696
- RK3588 datasheet: Rockchip RK3588 (8nm, 4×A76+4×A55, 6 TOPS NPU)
