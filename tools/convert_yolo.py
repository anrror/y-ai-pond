#!/usr/bin/env python3
"""
convert_yolo.py — YOLOv8n model conversion pipeline for edge deployment.

Converts a PyTorch YOLOv8n checkpoint to ONNX, then optionally to a
platform-specific format for edge inference:

    PT (.pt) → ONNX (.onnx)
    ONNX (.onnx) → RKNN (.rknn)   [for RK3588 NPU, INT8 quantized]
    ONNX (.onnx) → TensorRT (.engine)  [for Jetson GPU, FP16]

Requirements:
    pip install torch ultralytics onnx
    # For RK3588: pip install rknn-toolkit2 (Rockchip NPU SDK, Linux aarch64 only)
    # For Jetson:  trtexec CLI from TensorRT (pre-installed on JetPack)

Usage:
    python tools/convert_yolo.py --platform rk3588 --weights yolov8n.pt --output yolov8n.rknn
    python tools/convert_yolo.py --platform jetson  --weights yolov8n.pt --output yolov8n.engine
    python tools/convert_yolo.py --platform onnx    --weights yolov8n.pt --output yolov8n.onnx

This script is OFFLINE-ONLY and runs during model preparation, not in production.
"""

import argparse
import os
import subprocess
import sys
from pathlib import Path
from typing import Optional


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="YOLOv8n PT → ONNX → RKNN/TensorRT conversion pipeline",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  python tools/convert_yolo.py --platform onnx   --weights yolov8n.pt
  python tools/convert_yolo.py --platform rk3588 --weights yolov8n.pt --output yolov8n.rknn
  python tools/convert_yolo.py --platform jetson  --weights yolov8n.pt --output yolov8n_fp16.engine
        """,
    )
    parser.add_argument(
        "--platform",
        type=str,
        required=True,
        choices=["onnx", "rk3588", "jetson"],
        help="Target deployment platform: onnx (intermediate), rk3588 (Rockchip NPU), jetson (NVIDIA GPU)",
    )
    parser.add_argument(
        "--weights",
        type=str,
        required=True,
        help="Path to YOLOv8n PyTorch checkpoint (.pt file)",
    )
    parser.add_argument(
        "--output",
        type=str,
        default=None,
        help="Output file path (auto-generated if not specified)",
    )
    parser.add_argument(
        "--img-size",
        type=int,
        default=640,
        help="Input image size (default: 640, YOLOv8n standard)",
    )
    parser.add_argument(
        "--batch-size",
        type=int,
        default=1,
        help="Batch size for export (default: 1, edge inference is single-frame)",
    )
    parser.add_argument(
        "--opset",
        type=int,
        default=17,
        help="ONNX opset version (default: 17, for broad hardware compatibility)",
    )
    parser.add_argument(
        "--quantize",
        action="store_true",
        default=False,
        help="Enable INT8 quantization for RKNN (requires calibration dataset)",
    )
    parser.add_argument(
        "--calib-dataset",
        type=str,
        default=None,
        help="Path to calibration images directory (required if --quantize is set)",
    )
    return parser.parse_args()


def validate_inputs(args: argparse.Namespace) -> None:
    """Validate input arguments and file existence."""
    weights_path = Path(args.weights)
    if not weights_path.exists():
        print(f"[ERROR] Weights file not found: {args.weights}")
        sys.exit(1)
    if weights_path.suffix not in (".pt", ".pth"):
        print(f"[WARN] Expected .pt file, got {weights_path.suffix}")

    if args.quantize and not args.calib_dataset:
        print("[ERROR] --quantize requires --calib-dataset for calibration")
        sys.exit(1)

    if args.calib_dataset and not Path(args.calib_dataset).is_dir():
        print(f"[ERROR] Calibration dataset directory not found: {args.calib_dataset}")
        sys.exit(1)


def get_output_path(args: argparse.Namespace) -> str:
    """Determine the output file path based on platform."""
    if args.output:
        return args.output

    weights_stem = Path(args.weights).stem
    ext_map = {
        "onnx": ".onnx",
        "rk3588": ".rknn",
        "jetson": ".engine",
    }
    return weights_stem + ext_map[args.platform]


def export_onnx(args: argparse.Namespace) -> str:
    """
    Export YOLOv8n PyTorch model to ONNX format.

    Uses torch.onnx.export with dynamic batch and opset compatibility
    for edge hardware (RK3588 NPU requires opset ≤ 17).
    """
    onnx_path = Path(args.weights).with_suffix(".onnx")

    if args.platform == "onnx" and args.output:
        onnx_path = Path(args.output)

    if onnx_path.exists():
        print(f"[SKIP] ONNX already exists: {onnx_path}")
        return str(onnx_path)

    print(f"[STEP 1/2] Exporting PyTorch → ONNX (opset={args.opset})...")

    try:
        import torch
        from ultralytics import YOLO
    except ImportError as e:
        print(f"[ERROR] Missing dependency: {e}")
        print("  Install: pip install torch ultralytics onnx")
        sys.exit(1)

    model = YOLO(args.weights)
    print(f"  Loaded model: {args.weights}")

    success = model.export(
        format="onnx",
        imgsz=args.img_size,
        batch=args.batch_size,
        opset=args.opset,
        simplify=True,    # Simplify the ONNX graph
        dynamic=False,    # Fixed batch size for edge inference
        half=(args.platform == "jetson"),  # FP16 for Jetson
    )

    if not success:
        print("[ERROR] ONNX export failed")
        sys.exit(1)

    # ultralytics exports to <weights_stem>.onnx
    exported = Path(args.weights).with_suffix(".onnx")
    if args.output and args.platform == "onnx":
        os.rename(str(exported), str(onnx_path))
    else:
        onnx_path = exported

    file_size_mb = onnx_path.stat().st_size / (1024 * 1024)
    print(f"  ONNX exported: {onnx_path} ({file_size_mb:.1f} MB)")
    return str(onnx_path)


def convert_rknn(args: argparse.Namespace, onnx_path: str) -> str:
    """
    Convert ONNX to RKNN format for RK3588 NPU.

    Requires rknn-toolkit2 on Linux aarch64 (the RKNN SDK is
    only available on the target platform).
    """
    output_path = get_output_path(args)

    print(f"[STEP 2/2] Converting ONNX → RKNN ({output_path})...")

    try:
        from rknn.api import RKNN
    except ImportError:
        print("[ERROR] rknn-toolkit2 not installed.")
        print("  The RKNN SDK is only available on the RK3588 development board.")
        print("  Install: pip install rknn-toolkit2 (on the target device)")
        print(f"  Intermediate ONNX saved at: {onnx_path}")
        print("  Copy this ONNX to the RK3588 board and run:")
        print(f"    python tools/convert_yolo.py --platform rk3588 --weights {onnx_path} --output {output_path}")
        sys.exit(2)  # Exit code 2 = deferred (ONNX exists, RKNN conversion requires hardware)

    rknn = RKNN(verbose=True)

    # Configure RKNN for RK3588
    rknn.config(
        mean_values=[[0, 0, 0]],      # YOLOv8n normalization
        std_values=[[255, 255, 255]],
        target_platform="rk3588",
        quantized_dtype="w8a8" if args.quantize else "asymmetric_quantized-8",
    )

    print("  Loading ONNX model...")
    ret = rknn.load_onnx(model=onnx_path)
    if ret != 0:
        print(f"[ERROR] Failed to load ONNX: {onnx_path}")
        sys.exit(1)

    if args.quantize:
        print(f"  Building RKNN with INT8 quantization (calib: {args.calib_dataset})...")
        ret = rknn.build(
            do_quantization=True,
            dataset=args.calib_dataset,
        )
    else:
        print("  Building RKNN with default quantization...")
        ret = rknn.build(do_quantization=False)

    if ret != 0:
        print("[ERROR] RKNN build failed")
        sys.exit(1)

    print(f"  Exporting RKNN model to {output_path}...")
    ret = rknn.export_rknn(output_path)
    if ret != 0:
        print("[ERROR] RKNN export failed")
        sys.exit(1)

    rknn.release()
    file_size_mb = Path(output_path).stat().st_size / (1024 * 1024)
    print(f"  RKNN exported: {output_path} ({file_size_mb:.1f} MB)")
    return output_path


def convert_tensorrt(args: argparse.Namespace, onnx_path: str) -> str:
    """
    Convert ONNX to TensorRT engine for Jetson GPU (FP16).

    Uses the trtexec CLI tool from NVIDIA TensorRT, which is pre-installed
    on JetPack (Jetson Orin Nano / AGX).
    """
    output_path = get_output_path(args)

    print(f"[STEP 2/2] Converting ONNX → TensorRT Engine ({output_path})...")

    # Check if trtexec is available
    trtexec = "trtexec"
    try:
        subprocess.run([trtexec, "--version"], capture_output=True, check=True)
    except (subprocess.CalledProcessError, FileNotFoundError):
        print("[ERROR] trtexec not found.")
        print("  trtexec is part of NVIDIA TensorRT and is pre-installed on JetPack.")
        print("  To install manually: sudo apt install tensorrt")
        print(f"  Intermediate ONNX saved at: {onnx_path}")
        print("  Copy this ONNX to the Jetson board and run:")
        print(f"    /usr/src/tensorrt/bin/trtexec --onnx={onnx_path} --saveEngine={output_path} --fp16")
        sys.exit(2)  # Exit code 2 = deferred

    cmd = [
        trtexec,
        f"--onnx={onnx_path}",
        f"--saveEngine={output_path}",
        "--fp16",                       # FP16 for better perf on Jetson GPU
        f"--optShapes=images:1x3x{args.img_size}x{args.img_size}",
        "--workspace=2048",             # 2GB workspace
        "--verbose",
    ]

    print(f"  Running: {' '.join(cmd)}")
    result = subprocess.run(cmd, capture_output=True, text=True)

    if result.returncode != 0:
        print(f"[ERROR] trtexec failed:\n{result.stderr}")
        sys.exit(1)

    if Path(output_path).exists():
        file_size_mb = Path(output_path).stat().st_size / (1024 * 1024)
        print(f"  TensorRT engine exported: {output_path} ({file_size_mb:.1f} MB)")
    else:
        print(f"[ERROR] Output file not created: {output_path}")
        sys.exit(1)

    return output_path


def main() -> None:
    args = parse_args()
    validate_inputs(args)

    platform = args.platform
    output_path = get_output_path(args)

    print(f"YOLOv8n Conversion Pipeline")
    print(f"  Platform:  {platform}")
    print(f"  Weights:   {args.weights}")
    print(f"  Output:    {output_path}")
    print(f"  Image size: {args.img_size}x{args.img_size}")
    print()

    # Step 1: PT → ONNX (always)
    onnx_path = export_onnx(args)

    # Step 2: Platform-specific conversion
    if platform == "onnx":
        print("[DONE] ONNX export complete. No further conversion needed.")
        print(f"  Model ready for ONNX Runtime: {onnx_path}")

    elif platform == "rk3588":
        convert_rknn(args, onnx_path)
        print(f"[DONE] RKNN model ready for RK3588 NPU: {output_path}")
        print("  Deploy to: /data/models/yolov8n.rknn")
        print("  Go backend: use pkg/edge/detector.NewRKNBackend()")

    elif platform == "jetson":
        convert_tensorrt(args, onnx_path)
        print(f"[DONE] TensorRT engine ready for Jetson GPU: {output_path}")
        print("  Deploy to: /data/models/yolov8n_fp16.engine")
        print("  Go backend: use pkg/edge/detector.NewTensorRTBackend()")

    print()
    print("=== Conversion pipeline complete ===")


if __name__ == "__main__":
    main()
