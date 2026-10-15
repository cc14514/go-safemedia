// Package version 保存构建时注入的版本号，供 CLI 与桌面外壳共用。
package version

// Version 是当前版本号。
//
// 发布构建通过下列方式注入（见 Makefile 的 LDFLAGS）：
//
//	-ldflags "-X github.com/cc14514/go-safemedia/internal/version.Version=v0.2.0"
//
// 未注入时保持 dev 标记，便于区分自建包与发布包。
var Version = "0.2.0-dev"

// String 返回完整的版本展示文案。
func String() string { return "go-safemedia " + Version }
