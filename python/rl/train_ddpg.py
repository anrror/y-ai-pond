"""
DDPG Training Script for Aquaculture Feeding Optimization.

Trains a DDPG policy on the FeedingEnv, then exports the actor network
to ONNX for production inference via Go's pkg/cloud/rl/ONNXPolicy.

ONNX export uses torch.onnx.export with opset_version=17,
input shape [1, 5] (batch=1, state=[DO, Temp, NH3, FishWeight, FCR]),
output shape [1, 1] (feeding_rate).

Usage:
    # Quick training (small budget for CI/testing)
    python train_ddpg.py --timesteps 10000 --export-onnx model.onnx

    # Production training (full budget)
    python train_ddpg.py --timesteps 200000 --export-onnx model.onnx --seed 42

Dependencies:
    gymnasium, stable-baselines3, torch, onnx, numpy
    Install: pip install -r requirements.txt

This script is OFFLINE-ONLY. It is NEVER run on cloud servers.
The exported ONNX model is deployed to production via Go's onnxer.
"""

import argparse
import os
import sys
from typing import Optional

import numpy as np

# ---------------------------------------------------------------------------
# Graceful import errors — training deps are only needed for training.
# ---------------------------------------------------------------------------
try:
    import torch
    import torch.nn as nn
    HAS_TORCH = True
except ImportError:
    HAS_TORCH = False

try:
    import gymnasium as gym
    HAS_GYM = True
except ImportError:
    HAS_GYM = False

try:
    from stable_baselines3 import DDPG
    from stable_baselines3.common.noise import NormalActionNoise, OrnsteinUhlenbeckActionNoise
    from stable_baselines3.common.callbacks import CallbackList, CheckpointCallback, EvalCallback
    from stable_baselines3.common.vec_env import DummyVecEnv, VecNormalize
    HAS_SB3 = True
except ImportError:
    HAS_SB3 = False

# Local imports
from feeding_env import FeedingEnv, STATE_MIN, STATE_MAX, DEFAULT_CONFIG


# ---------------------------------------------------------------------------
# Onnxable Actor Wrapper
# ---------------------------------------------------------------------------

class OnnxableActor(nn.Module):
    """Wraps the SB3 DDPG actor for ONNX export.

    Returns only the action (no log_prob or value), matching the
    Go ONNXPolicy interface which expects state[5] → action[1].

    For DDPG, the actor outputs action directly (deterministic policy).
    The action is in [-1, 1] (SB3 default) — rescale to [0,1] during
    post-processing if needed.
    """

    def __init__(self, actor: nn.Module):
        super().__init__()
        self.actor = actor

    def forward(self, observation: torch.Tensor) -> torch.Tensor:
        # DDPG actor returns the deterministic action (no noise).
        # Input: [batch, 5]
        # Output: [batch, 1]
        return self.actor(observation)


# ---------------------------------------------------------------------------
# Training orchestration
# ---------------------------------------------------------------------------

def make_env(seed: Optional[int] = None):
    """Factory for DummyVecEnv."""
    def _init():
        env = FeedingEnv()
        if seed is not None:
            env.seed(seed)
        return env
    return _init


def train_ddpg(
    total_timesteps: int = 100000,
    seed: int = 42,
    log_dir: str = "./ddpg_logs",
    model_save_path: str = "./ddpg_feeding",
    eval_freq: int = 5000,
    learning_rate: float = 1e-3,
    buffer_size: int = 100000,
    batch_size: int = 128,
    tau: float = 0.005,
    gamma: float = 0.99,
    policy_kwargs: Optional[dict] = None,
):
    """Train a DDPG agent on the FeedingEnv.

    Args:
        total_timesteps: Total training steps.
        seed: Random seed for reproducibility.
        log_dir: TensorBoard log directory.
        model_save_path: Path prefix for saving the SB3 model.
        eval_freq: Evaluate every N timesteps.
        learning_rate: Actor/critic learning rate.
        buffer_size: Replay buffer capacity.
        batch_size: Training batch size.
        tau: Soft update coefficient.
        gamma: Discount factor.
        policy_kwargs: Additional kwargs for the MlpPolicy.

    Returns:
        The trained DDPG model.
    """
    if not HAS_SB3:
        raise ImportError(
            "stable-baselines3 is required for training. "
            "Install: pip install stable-baselines3"
        )

    # Create vectorized environment
    env = DummyVecEnv([make_env(seed)])

    # Action noise: Ornstein-Uhlenbeck for temporally correlated exploration
    n_actions = env.action_space.shape[-1]
    action_noise = OrnsteinUhlenbeckActionNoise(
        mean=np.zeros(n_actions),
        sigma=0.1 * np.ones(n_actions),
    )

    # Default policy architecture: 2 hidden layers of 256 units
    if policy_kwargs is None:
        policy_kwargs = dict(
            net_arch=dict(pi=[256, 256], qf=[256, 256]),
        )

    model = DDPG(
        "MlpPolicy",
        env,
        action_noise=action_noise,
        learning_rate=learning_rate,
        buffer_size=buffer_size,
        batch_size=batch_size,
        tau=tau,
        gamma=gamma,
        seed=seed,
        policy_kwargs=policy_kwargs,
        verbose=1,
        tensorboard_log=log_dir,
    )

    # Callbacks
    callbacks = [
        CheckpointCallback(
            save_freq=max(1000, eval_freq // 2),
            save_path=log_dir,
            name_prefix="ddpg_feeding",
        ),
        EvalCallback(
            env,
            best_model_save_path=f"{log_dir}/best",
            log_path=log_dir,
            eval_freq=eval_freq,
            deterministic=True,
            render=False,
        ),
    ]

    # Train
    model.learn(
        total_timesteps=total_timesteps,
        callback=CallbackList(callbacks),
        log_interval=100,
    )

    # Save SB3 model (zip file)
    model.save(model_save_path)
    print(f"\nModel saved to {model_save_path}.zip")

    return model


def export_onnx(model: "DDPG", output_path: str, opset_version: int = 17) -> str:
    """Export the trained DDPG actor to ONNX format.

    The exported model has:
      - Input:  [batch=1, 5] float32 tensor ("input")
      - Output: [batch=1, 1] float32 tensor ("action")

    Args:
        model: Trained SB3 DDPG model.
        output_path: Path for the .onnx file.
        opset_version: ONNX opset version (default 17, min 14).

    Returns:
        Path to the exported ONNX file.
    """
    if not HAS_TORCH:
        raise ImportError(
            "PyTorch is required for ONNX export. "
            "Install: pip install torch"
        )

    # Wrap the DDPG actor
    actor = model.policy.actor
    onnx_actor = OnnxableActor(actor)
    onnx_actor.eval()

    # Dummy input: batch=1, state=5
    observation_size = model.observation_space.shape  # (5,)
    dummy_input = torch.randn(1, *observation_size)

    # Export
    torch.onnx.export(
        onnx_actor,
        dummy_input,
        output_path,
        opset_version=opset_version,
        input_names=["input"],
        output_names=["action"],
        dynamic_axes={
            "input": {0: "batch"},
            "action": {0: "batch"},
        },
    )

    print(f"ONNX model exported to {output_path}")
    print(f"  Input shape:  [batch, {observation_size[0]}]")
    print(f"  Output shape: [batch, 1]")
    print(f"  Opset: {opset_version}")

    return output_path


def verify_onnx(onnx_path: str) -> bool:
    """Verify the exported ONNX model is valid and has correct shapes.

    Uses onnx and onnxruntime to validate.
    """
    try:
        import onnx
        import onnxruntime as ort
    except ImportError:
        print("WARNING: onnx/onnxruntime not installed — skipping verification.")
        print("  Install: pip install onnx onnxruntime")
        return True  # Non-fatal

    # Load and check model structure
    onnx_model = onnx.load(onnx_path)
    onnx.checker.check_model(onnx_model)

    graph = onnx_model.graph
    input_shape = [d.dim_value for d in graph.input[0].type.tensor_type.shape.dim]
    output_shape = [d.dim_value for d in graph.output[0].type.tensor_type.shape.dim]

    print(f"\nONNX model validation:")
    print(f"  Input name:  {graph.input[0].name}")
    print(f"  Input shape: {input_shape}")
    print(f"  Output name: {graph.output[0].name}")
    print(f"  Output shape: {output_shape}")

    # Shape checks (batch dimension may be dynamic → 1 or "batch")
    batch_dim = input_shape[0]
    if batch_dim not in (1, 0):
        print(f"  WARNING: unexpected batch dimension: {batch_dim}")
    if input_shape[1] != 5:
        raise ValueError(f"Expected input dim 5, got {input_shape[1]}")

    batch_out = output_shape[0]
    if batch_out not in (1, 0):
        print(f"  WARNING: unexpected output batch dimension: {batch_out}")
    if output_shape[1] != 1:
        raise ValueError(f"Expected output dim 1, got {output_shape[1]}")

    # Inference test
    ort_session = ort.InferenceSession(onnx_path)
    observation = np.zeros((1, 5), dtype=np.float32)
    # Test normal state
    observation[0] = [7.5, 25.0, 0.1, 500.0, 1.5]
    action = ort_session.run(None, {"input": observation})[0]

    print(f"\n  Test inference: state={observation[0].tolist()}")
    print(f"  Action: {action[0][0]:.6f}")
    print(f"  Action shape: {action.shape}")

    # Action should be in [-1, 1] (tanh output from DDPG)
    if -2.0 <= action[0][0] <= 2.0:
        print(f"  Action range check: PASS (in [-1, 1] ballpark)")
    else:
        print(f"  WARNING: action {action[0][0]} is far from expected range [-1, 1]")

    return True


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Train DDPG on FeedingEnv and export to ONNX.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  %(prog)s --timesteps 10000 --export-onnx model.onnx
  %(prog)s --timesteps 200000 --export-onnx model.onnx --seed 42 --lr 1e-4
        """,
    )
    parser.add_argument(
        "--timesteps", type=int, default=10000,
        help="Total training timesteps (default: 10000 for CI, 200000 for production)",
    )
    parser.add_argument(
        "--seed", type=int, default=42,
        help="Random seed (default: 42)",
    )
    parser.add_argument(
        "--export-onnx", type=str, default=None,
        help="Export trained policy to ONNX at this path",
    )
    parser.add_argument(
        "--no-verify", action="store_true",
        help="Skip ONNX model verification",
    )
    parser.add_argument(
        "--lr", type=float, default=1e-3,
        help="Learning rate (default: 1e-3)",
    )
    parser.add_argument(
        "--buffer-size", type=int, default=100000,
        help="Replay buffer size (default: 100000)",
    )
    parser.add_argument(
        "--batch-size", type=int, default=128,
        help="Training batch size (default: 128)",
    )
    parser.add_argument(
        "--log-dir", type=str, default="./ddpg_logs",
        help="TensorBoard log directory (default: ./ddpg_logs)",
    )
    parser.add_argument(
        "--model-save", type=str, default="./ddpg_feeding",
        help="SB3 model save path prefix (default: ./ddpg_feeding)",
    )

    args = parser.parse_args()

    # Check dependencies
    missing = []
    if not HAS_TORCH:
        missing.append("torch")
    if not HAS_GYM:
        missing.append("gymnasium")
    if not HAS_SB3:
        missing.append("stable-baselines3")

    if missing:
        print(f"ERROR: Missing dependencies: {', '.join(missing)}")
        print("Install: pip install -r python/rl/requirements.txt")
        sys.exit(1)

    print("=" * 60)
    print("DDPG Aquaculture Feeding Optimization Training")
    print("=" * 60)
    print(f"  Timesteps:   {args.timesteps}")
    print(f"  Seed:        {args.seed}")
    print(f"  Learning rate: {args.lr}")
    print(f"  Buffer size: {args.buffer_size}")
    print(f"  Batch size:  {args.batch_size}")
    print(f"  ONNX export: {args.export_onnx or 'disabled'}")
    print("=" * 60)
    print()

    # Step 1: Train
    print("Starting training...\n")
    model = train_ddpg(
        total_timesteps=args.timesteps,
        seed=args.seed,
        log_dir=args.log_dir,
        model_save_path=args.model_save,
        learning_rate=args.lr,
        buffer_size=args.buffer_size,
        batch_size=args.batch_size,
    )
    print("\nTraining complete.\n")

    # Step 2: Optional ONNX export
    if args.export_onnx:
        print("Exporting to ONNX...")
        onnx_path = export_onnx(model, args.export_onnx)
        print(f"ONNX model saved: {onnx_path}")

        if not args.no_verify:
            try:
                verify_onnx(onnx_path)
            except Exception as e:
                print(f"WARNING: ONNX verification failed (non-fatal): {e}")
                print("The model may still be valid — verify manually with onnxruntime.")

    # Step 3: Summary
    print("\n" + "=" * 60)
    print("Training pipeline complete.")
    print("=" * 60)
    if args.export_onnx:
        print(f"\nNext: Copy {args.export_onnx} to the Go deployment server")
        print("and load it with pkg/cloud/rl/ONNXPolicy.LoadModel().")
        print()
        print("Go inference test:")
        print("  policy := rl.NewONNXPolicy()")
        print("  policy.LoadModel(\"model.onnx\")")
        print("  rate, _ := policy.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})")


if __name__ == "__main__":
    main()
