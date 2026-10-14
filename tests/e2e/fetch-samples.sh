#!/usr/bin/env bash
# 下载 L3 用的真实视频样本（小体积、公开可访问）。
#
#   tests/e2e/fetch-samples.sh <目标目录>
#   SAFEMEDIA_E2E_SAMPLES=<目标目录> make test-l3
#
# 覆盖的容器/编码：
#   - mp4  (H.264)：Chrome 可解码，用于「密档流能真实播放」的断言
#   - mov  (QuickTime)：验证 .mov 容器识别与 video/quicktime 流
#   - webm (VP8/VP9)：验证 WebM 路径
#   注：需要 iPhone 实机 .mov（H.264/HEVC）与 Firefox HEVC 提示，见 REPORT.md「待人工验证」。
set -euo pipefail

DEST="${1:-}"
if [[ -z "$DEST" ]]; then
  echo "用法: $0 <目标目录>" >&2
  exit 2
fi
mkdir -p "$DEST"
cd "$DEST"

fetch() { # fetch <url> <文件名>
  local url="$1" name="$2"
  echo "↓ $name"
  curl -fsSL --max-time 120 -o "$name" "$url"
  if [[ ! -s "$name" ]]; then
    echo "✗ 下载失败或为空: $name" >&2
    return 1
  fi
}

fetch "https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/Big_Buck_Bunny_360_10s_1MB.mp4" "Big_Buck_Bunny_360_10s_1MB.mp4"
fetch "https://filesamples.com/samples/video/mp4/sample_640x360.mp4" "sample_640x360.mp4"
fetch "https://filesamples.com/samples/video/mov/sample_640x360.mov" "sample_640x360.mov"
fetch "https://filesamples.com/samples/video/webm/sample_640x360.webm" "sample_640x360.webm"

echo
echo "✓ 样本就绪：$DEST"
ls -lh
echo
echo "运行：SAFEMEDIA_E2E_SAMPLES=$DEST make test-l3"