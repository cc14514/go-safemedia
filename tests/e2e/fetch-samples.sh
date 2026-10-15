#!/usr/bin/env bash
# 准备 L3 用的真实视频样本（小体积、公开可访问）。
#
#   tests/e2e/fetch-samples.sh <目标目录>
#   SAFEMEDIA_E2E_SAMPLES=<目标目录> make test-l3
#
# 覆盖的容器/编码：
#   - mp4  (H.264/AAC) ：Chrome 可解码，用于「密档流能真实播放」的断言
#   - webm (VP9)       ：验证 WebM 路径
#   - mov  (QuickTime) ：验证 .mov 容器识别与 video/quicktime 流
#   注：iPhone 实机 .mov（H.264/HEVC）与 Firefox 的 HEVC 提示需人工确认，见 REPORT.md「待人工验证」。
set -euo pipefail

DEST="${1:-}"
if [[ -z "$DEST" ]]; then
  echo "用法: $0 <目标目录>" >&2
  exit 2
fi
mkdir -p "$DEST"
cd "$DEST"

fetch() { # fetch <url> <文件名>：下载失败返回非零
  local url="$1" name="$2"
  echo "↓ $name  ← $url"
  curl -fsSL --connect-timeout 15 --max-time 240 -o "$name" "$url"
  if [[ ! -s "$name" ]]; then
    echo "✗ 下载失败或为空: $name" >&2
    return 1
  fi
}

fetch_first() { # fetch_first <文件名> <url>...：按顺序试，第一个成功即返回
  local name="$1"; shift
  local u
  for u in "$@"; do
    if fetch "$u" "$name"; then return 0; fi
    rm -f "$name"
  done
  return 1
}

MP4="Big_Buck_Bunny_360_10s_1MB.mp4"
fetch_first "$MP4" \
  "https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/360/$MP4" \
  "https://download.samplelib.com/mp4/sample-5s.mp4"

fetch_first "Big_Buck_Bunny_360_10s_1MB.webm" \
  "https://download.samplelib.com/webm/sample-5s.webm" \
  "https://test-videos.co.uk/vids/bigbuckbunny/webm/vp9/360/Big_Buck_Bunny_360_10s_1MB.webm" \
  || echo "⚠ 拿不到 webm 样本，L3 将跳过 WebM 行" >&2

# mov：优先公开镜像；镜像不可达时由 mp4 重标 ftyp major_brand 生成 QuickTime 样本
# （同一种 ISO BMFF 结构、同样的 H.264/AAC 轨道，只是容器品牌标记为 'qt  '）。
if ! fetch_first "sample_640x360.mov" "https://filesamples.com/samples/video/mov/sample_640x360.mov"; then
  echo "⚠ 无 .mov 镜像可用 → 由 $MP4 生成 QuickTime 样本 bbb_qt.mov（重标 ftyp brand 为 'qt  '）"
  cp "$MP4" bbb_qt.mov
  printf 'qt  ' | dd of=bbb_qt.mov bs=1 seek=8 conv=notrunc 2>/dev/null
fi

echo
echo "✓ 样本就绪：$DEST"
ls -lh
echo
echo "运行：SAFEMEDIA_E2E_SAMPLES=$DEST make test-l3"