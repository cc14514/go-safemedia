package vault

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrUnsupportedContainer 表示文件不是白名单容器（mp4 / mov / webm）。
var ErrUnsupportedContainer = errors.New("不支持的视频容器：仅支持 mp4、mov、webm")

// sniffLen 是嗅探容器所需的字节数（足以覆盖 ftyp brand 与 EBML DocType）。
const sniffLen = 512

// DetectContainer 依据明文头部字节判断容器类型，返回头部提示常量与可读名称。
//
// mp4 / mov：ISO BMFF，以 `ftyp` box 起始；major_brand = "qt  " 视为 mov。
// webm / mkv：EBML，以 1A 45 DF A3 起始，靠 DocType "webm" / "matroska" 区分；
// 只有 webm 在白名单内，mkv 明确拒绝（浏览器无法原生播放）。
func DetectContainer(head []byte) (hint byte, name string, err error) {
	if len(head) >= 12 && bytes.Equal(head[4:8], []byte("ftyp")) {
		brand := string(head[8:12])
		if brand == "qt  " {
			return ContainerMOV, "mov", nil
		}
		return ContainerMP4, "mp4", nil
	}
	if len(head) >= 4 && bytes.Equal(head[0:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		if bytes.Contains(head, []byte("webm")) && !bytes.Contains(head, []byte("matroska")) {
			return ContainerWebM, "webm", nil
		}
		if bytes.Contains(head, []byte("matroska")) {
			return 0, "", fmt.Errorf("%w: 检测到 mkv(matroska)", ErrUnsupportedContainer)
		}
		return 0, "", fmt.Errorf("%w: 未知 EBML 文档类型", ErrUnsupportedContainer)
	}
	return 0, "", ErrUnsupportedContainer
}

// ContainerContentType 返回容器对应的 MIME 类型（播放服务的 fallback）。
func ContainerContentType(hint byte) string {
	switch hint {
	case ContainerMP4:
		return "video/mp4"
	case ContainerMOV:
		return "video/quicktime"
	case ContainerWebM:
		return "video/webm"
	}
	return "application/octet-stream"
}

// SniffContentType 优先按解密后明文首字节嗅探（plan.md §6），失败才回退到头部提示。
func SniffContentType(plainHead []byte, fallbackHint byte) string {
	if hint, _, err := DetectContainer(plainHead); err == nil {
		return ContainerContentType(hint)
	}
	return ContainerContentType(fallbackHint)
}
