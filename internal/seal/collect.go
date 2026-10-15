// Package seal 是封存（seal）的输入展开与队列执行：CLI 与桌面外壳共用一份实现，
// 两处只负责各自的呈现（终端逐条打印 / 页面进度面板）。
//
// 明文只在内存中流转：本包不产生任何明文文件，失败时按调用方选择清理未完成的密档。
package seal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// VaultExt 是密档扩展名。
const VaultExt = ".svid"

// Job 是一次封存：源视频路径与目标密档路径。
type Job struct {
	Src string
	Dst string
}

// Reject 是一个未被展开为任务（或被拒绝）的输入，供页面逐条展示原因。
//
// Fatal 为真表示**显式给出的输入**不可用：CLI 据此以用户错误退出（ExitUser），
// 页面则把它当作条目级失败，其余条目照常进行（AC10.4）。
type Reject struct {
	Path   string
	Reason string
	Fatal  bool
}

// Result 是 Collect 的展开结果。
type Result struct {
	Jobs    []Job
	Rejects []Reject
	Skipped int   // 目录遍历中跳过的非视频数量（CLI 用它打印「跳过 N 个非视频文件」）
	Err     error // 首个致命输入错误（CLI 直接返回它；页面忽略并改用 Rejects）
	// UserError 为真表示 Err 属于「用户输入问题」（CLI 以 ExitUser 退出），
	// 为假表示遍历/IO 类错误（CLI 以普通失败退出）。
	UserError bool
}

// Collect 把输入（视频文件或目录）展开为封存任务。
//
// out 语义与 CLI 的 -o 完全一致（AC10.11）：
//   - out 为空：密档写在源文件旁边（clip.mp4 → clip.mp4.svid）
//   - 单个文件输入且 out 不是已存在的目录：out 视为密档文件路径
//   - 目录输入 / 多个输入 / out 本身是目录：out 视为输出目录，源目录的相对子路径在此重建
//
// 目录输入递归遍历，隐藏项、符号链接、非白名单容器、已有 .svid 一律跳过并记录原因。
// 显式给出的文件白名单不通过则记录为 Fatal（CLI 视为用户错误）。
func Collect(inputs []string, out string) (Result, error) {
	var res Result

	outIsDir := out != ""
	if outIsDir && len(inputs) == 1 {
		if info, err := os.Stat(inputs[0]); err == nil && info.IsDir() {
			outIsDir = true
		} else if info, err := os.Stat(out); err != nil || !info.IsDir() {
			outIsDir = false // 单个文件 + -o 指向不存在的路径 → 密档文件
		}
	}

	seen := make(map[string]bool)
	add := func(j Job) {
		abs, err := filepath.Abs(j.Src)
		if err != nil {
			abs = j.Src
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		res.Jobs = append(res.Jobs, j)
	}

	for _, arg := range inputs {
		info, err := os.Stat(arg)
		if err != nil {
			e := fmt.Errorf("%s: %w（若要指定输出位置，请用 -o）", arg, err)
			res.Rejects = append(res.Rejects, Reject{Path: arg, Reason: e.Error(), Fatal: true})
			if res.Err == nil {
				res.Err = e
				res.UserError = true
			}
			continue
		}

		if info.IsDir() {
			jobs, rejects, walkErr := walkDir(arg, out, outIsDir)
			res.Rejects = append(res.Rejects, rejects...)
			if walkErr != nil && res.Err == nil {
				res.Err = walkErr // 与 CLI 一致：原样返回遍历错误（不额外包装）
			}
			for _, j := range jobs {
				add(j)
			}
			continue
		}

		// 显式给出的文件：白名单不通过就直接报错（而不是静默跳过）。
		if _, _, err := vault.DetectFileContainer(arg); err != nil {
			e := fmt.Errorf("%s: %w", arg, err)
			res.Rejects = append(res.Rejects, Reject{Path: arg, Reason: e.Error(), Fatal: true})
			if res.Err == nil {
				res.Err = e
				res.UserError = true
			}
			continue
		}
		add(Job{Src: arg, Dst: fileOutput(arg, out, outIsDir)})
	}

	sort.Slice(res.Jobs, func(i, j int) bool { return res.Jobs[i].Src < res.Jobs[j].Src })
	for _, r := range res.Rejects {
		if !r.Fatal {
			res.Skipped++
		}
	}
	return res, res.Err
}

// walkDir 递归遍历目录，收集可封存的视频文件。
func walkDir(root, out string, outIsDir bool) ([]Job, []Reject, error) {
	var jobs []Job
	var rejects []Reject

	// 输出目录若位于输入目录之内，遍历时跳过它本身（避免把上一轮的密档再封一遍）。
	skipDirs := make(map[string]bool)
	if outIsDir && out != "" {
		if abs, err := filepath.Abs(out); err == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				skipDirs[abs] = true
			}
		}
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			rejects = append(rejects, Reject{Path: path, Reason: err.Error(), Fatal: true})
			return err // 与 CLI 一致：遍历出错即中止（调用方以 err 退出）
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // 不跟随符号链接
		}
		if d.IsDir() {
			if abs, aerr := filepath.Abs(path); aerr == nil && skipDirs[abs] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), VaultExt) {
			return nil
		}
		if _, _, err := vault.DetectFileContainer(path); err != nil {
			rejects = append(rejects, Reject{Path: path, Reason: "非白名单容器（仅支持 mp4 / mov / webm）"})
			return nil
		}

		var dst string
		if outIsDir && out != "" {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = d.Name()
			}
			dst = filepath.Join(out, rel) + VaultExt
		} else {
			dst = path + VaultExt
		}
		jobs = append(jobs, Job{Src: path, Dst: dst})
		return nil
	})
	return jobs, rejects, walkErr
}

// fileOutput 计算单个文件的输出密档路径。
func fileOutput(src, out string, outIsDir bool) string {
	switch {
	case out == "":
		return src + VaultExt
	case outIsDir:
		return filepath.Join(out, filepath.Base(src)+VaultExt)
	default:
		return out
	}
}
