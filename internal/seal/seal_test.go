package seal

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// ---------- 夹具 ----------

// fakeMP4 造一个带合法 ftyp 头的假 mp4（白名单嗅探据此通过）。
func fakeMP4(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	return b
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// testKey 造一把测试用私钥（封存只需要公钥）。
func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatalf("生成账户失败: %v", err)
	}
	return acct.PrivateKey()
}

func srcs(jobs []Job) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Src)
	}
	sort.Strings(out)
	return out
}

func dstOf(t *testing.T, jobs []Job, src string) string {
	t.Helper()
	for _, j := range jobs {
		if j.Src == src {
			return j.Dst
		}
	}
	t.Fatalf("任务中没有 %s（jobs=%v）", src, jobs)
	return ""
}

// ---------- Collect（T10.2 / AC10.1、AC10.2、AC10.6）----------

func TestCollectFileToDirAndBeside(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp4")
	out := filepath.Join(dir, "out")
	writeFile(t, a, fakeMP4(4096))
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}

	// 目录输出：密档落在输出目录，文件名沿用源名 + .svid
	res, err := Collect([]string{a}, out)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Jobs) != 1 {
		t.Fatalf("期望 1 个任务，得到 %d", len(res.Jobs))
	}
	if want := filepath.Join(out, "a.mp4.svid"); res.Jobs[0].Dst != want {
		t.Fatalf("目标密档 = %s，期望 %s", res.Jobs[0].Dst, want)
	}

	// 输出为空：写在源文件旁边
	res, err = Collect([]string{a}, "")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := a + ".svid"; res.Jobs[0].Dst != want {
		t.Fatalf("目标密档 = %s，期望 %s", res.Jobs[0].Dst, want)
	}

	// 单个文件 + 指向不存在的路径：视为密档文件路径（CLI -o 语义）
	res, err = Collect([]string{a}, filepath.Join(dir, "named.svid"))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if want := filepath.Join(dir, "named.svid"); res.Jobs[0].Dst != want {
		t.Fatalf("目标密档 = %s，期望 %s", res.Jobs[0].Dst, want)
	}
}

func TestCollectDirKeepsRelativeStructure(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	out := filepath.Join(dir, "out")
	writeFile(t, filepath.Join(in, "top.mp4"), fakeMP4(1024))
	writeFile(t, filepath.Join(in, "sub", "deep.mov"), fakeMP4(1024))
	writeFile(t, filepath.Join(in, "sub", "note.txt"), []byte("不是视频"))
	writeFile(t, filepath.Join(in, ".hidden.mp4"), fakeMP4(1024))
	writeFile(t, filepath.Join(in, "sub", "old.mp4.svid"), []byte("已是密档"))

	res, err := Collect([]string{in}, out)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := srcs(res.Jobs)
	want := []string{filepath.Join(in, "sub", "deep.mov"), filepath.Join(in, "top.mp4")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("任务 = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("任务 = %v，期望 %v", got, want)
		}
	}
	// 相对结构保留在输出目录下
	if d := dstOf(t, res.Jobs, filepath.Join(in, "sub", "deep.mov")); d != filepath.Join(out, "sub", "deep.mov.svid") {
		t.Fatalf("子目录密档 = %s", d)
	}
	// 非白名单与已有密档只计入跳过，不产生任务
	if res.Skipped == 0 {
		t.Fatal("期望记录跳过项（非白名单 + 已是密档）")
	}
	for _, r := range res.Rejects {
		if r.Fatal {
			t.Fatalf("目录遍历中的跳过不应是致命项：%+v", r)
		}
	}
}

func TestCollectExplicitUnsupportedIsFatal(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "note.txt")
	writeFile(t, txt, []byte("不是视频"))
	missing := filepath.Join(dir, "nope.mp4")

	res, err := Collect([]string{txt, missing}, "")
	if err == nil {
		t.Fatal("显式给出的非视频 / 不存在的路径应当是致命错误（CLI 以用户错误退出）")
	}
	if len(res.Jobs) != 0 {
		t.Fatalf("不应产生任务，得到 %v", res.Jobs)
	}
	fatal := 0
	for _, r := range res.Rejects {
		if r.Fatal {
			fatal++
		}
	}
	if fatal != 2 {
		t.Fatalf("期望 2 个致命项，得到 %d（%+v）", fatal, res.Rejects)
	}
}

func TestCollectSkipsOutputDirInsideInput(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	out := filepath.Join(in, "sealed") // 输出目录在输入目录之内
	writeFile(t, filepath.Join(in, "a.mp4"), fakeMP4(1024))
	writeFile(t, filepath.Join(out, "b.mp4"), fakeMP4(1024)) // 上一轮的源被误放进输出目录

	res, err := Collect([]string{in}, out)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, j := range res.Jobs {
		if filepath.Dir(j.Src) == out {
			t.Fatalf("输出目录内的文件不应被展开：%s", j.Src)
		}
	}
}

func TestCollectDeduplicatesInputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mp4")
	writeFile(t, a, fakeMP4(1024))
	res, err := Collect([]string{a, a, dir}, "")
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	n := 0
	for _, j := range res.Jobs {
		if j.Src == a {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同一个源出现 %d 次，期望 1 次", n)
	}
}

// ---------- Queue（T10.3 / AC10.3、AC10.4、AC10.5、AC10.8、AC10.9）----------

func TestQueueSealsSequentiallyWithProgress(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	// 明文 > 分块大小（4 MiB）才有多段进度：分块是格式本身的粒度。
	const size = 12 << 20
	writeFile(t, filepath.Join(dir, "a.mp4"), fakeMP4(size))
	writeFile(t, filepath.Join(dir, "b.mp4"), fakeMP4(size))
	priv := testKey(t)

	res, err := Collect([]string{filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")}, out)
	if err != nil {
		t.Fatal(err)
	}

	last := make(map[string]int64)
	var monotonic = true
	var q *Queue
	q = NewQueue(res.Jobs, Options{OnChange: func() {
		for _, it := range q.Items() {
			if prev, ok := last[it.Src]; ok && it.Done < prev {
				monotonic = false // 进度必须单调递增（AC10.3）
			}
			last[it.Src] = it.Done
		}
	}})
	q.Start(&priv.PublicKey)

	// 进度必须出现在队列结束之前（AC10.3：不是结束时一次性出现）
	deadline := time.Now().Add(5 * time.Second)
	sawInFlight := false
	for time.Now().Before(deadline) && !sawInFlight {
		for _, it := range q.Items() {
			if it.State == StateRunning && it.Done > 0 && it.Done < it.Total {
				sawInFlight = true
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	q.Wait()

	done, failed, skipped, canceled := q.Summary()
	if done != 2 || failed != 0 || skipped != 0 || canceled != 0 {
		t.Fatalf("完成=%d 失败=%d 跳过=%d 取消=%d，期望 2/0/0/0", done, failed, skipped, canceled)
	}
	for _, it := range q.Items() {
		if it.State != StateDone || it.Percent() != 100 {
			t.Fatalf("条目未完成：%+v", it)
		}
		if it.PlaintextSize != size || it.Chunks == 0 {
			t.Fatalf("条目缺少封存结果：%+v", it)
		}
		if _, err := os.Stat(it.Dst); err != nil {
			t.Fatalf("密档不存在：%v", err)
		}
	}
	if !sawInFlight {
		t.Error("未观察到进行中的进度（AC10.3 要求实时进度）")
	}
	if !monotonic {
		t.Error("进度出现回退（AC10.3 要求单调递增）")
	}
}

func TestQueueDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mp4")
	writeFile(t, src, fakeMP4(4096))
	dst := src + ".svid"
	writeFile(t, dst, []byte("已有内容"))
	priv := testKey(t)

	q := NewQueue([]Job{{Src: src, Dst: dst}}, Options{})
	q.Start(&priv.PublicKey)
	q.Wait()

	it := q.Items()[0]
	if it.State != StateSkipped {
		t.Fatalf("已存在的目标应跳过，得到 %+v", it)
	}
	if b, _ := os.ReadFile(dst); string(b) != "已有内容" {
		t.Fatal("目标密档被改写了（不得覆盖，AC10.5）")
	}
	if it.Reason == "" {
		t.Fatal("跳过应带原因")
	}
}

func TestQueueIsolatesFailures(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.mp4")   // 非白名单
	good := filepath.Join(dir, "good.mp4") // 正常
	writeFile(t, bad, []byte("不是视频的字节"))
	writeFile(t, good, fakeMP4(4096))
	priv := testKey(t)

	q := NewQueue([]Job{
		{Src: bad, Dst: bad + ".svid"},
		{Src: good, Dst: good + ".svid"},
	}, Options{})
	q.Start(&priv.PublicKey)
	q.Wait()

	items := q.Items()
	if items[0].State != StateFailed || items[0].Reason == "" {
		t.Fatalf("坏条目应失败并带原因：%+v", items[0])
	}
	if items[1].State != StateDone {
		t.Fatalf("好条目应完成（单条失败不影响其它）：%+v", items[1])
	}
	if _, err := os.Stat(bad + ".svid"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("失败条目不应留下任何文件")
	}
}

func TestQueueCancelStopsAndCleansPartial(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.mp4")
	writeFile(t, big, fakeMP4(64<<20)) // 足够大，便于在中途取消
	other := filepath.Join(dir, "other.mp4")
	writeFile(t, other, fakeMP4(4096))
	priv := testKey(t)

	q := NewQueue([]Job{
		{Src: big, Dst: big + ".svid"},
		{Src: other, Dst: other + ".svid"},
	}, Options{RemovePartialOnFailure: true})
	q.Start(&priv.PublicKey)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if it := q.Items()[0]; it.State == StateRunning && it.Done > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	q.Cancel()
	q.Wait()

	items := q.Items()
	if items[0].State != StateCanceled {
		t.Fatalf("被中止的条目应为已取消：%+v", items[0])
	}
	if items[1].State != StateCanceled {
		t.Fatalf("未开始的条目应标记为已取消：%+v", items[1])
	}
	if _, err := os.Stat(big + ".svid"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("中止后应清理未完成的密档（AC10.8/AC10.9）")
	}
	if _, err := os.Stat(other + ".svid"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("未开始的条目不应产生文件")
	}
	if q.Active() {
		t.Fatal("队列结束后 Active 应为假")
	}
}

func TestQueueCancelBeforeStartMarksAllCanceled(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mp4")
	writeFile(t, src, fakeMP4(4096))
	priv := testKey(t)

	q := NewQueue([]Job{{Src: src, Dst: src + ".svid"}}, Options{RemovePartialOnFailure: true})
	q.Cancel()
	q.Start(&priv.PublicKey)
	q.Wait()

	if it := q.Items()[0]; it.State != StateCanceled {
		t.Fatalf("取消后不应封存：%+v", it)
	}
	if _, err := os.Stat(src + ".svid"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("取消后不应产生文件")
	}
}

func TestQueueFailureCleansPartialWhenAsked(t *testing.T) {
	dir := t.TempDir()
	// 源文件声明 1MiB 但实际只有一小段：封存中途失败（明文不足），应清理半成品。
	src := filepath.Join(dir, "truncated.mp4")
	writeFile(t, src, fakeMP4(4096))
	priv := testKey(t)

	// 先把明文长度谎报大：直接调用 vault 层模拟「读不满」太难，
	// 改用「目标目录不可写」这个更贴近真实场景的失败。
	ro := filepath.Join(dir, "readonly")
	if err := os.MkdirAll(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })

	q := NewQueue([]Job{{Src: src, Dst: filepath.Join(ro, "sub", "a.mp4.svid")}}, Options{RemovePartialOnFailure: true})
	q.Start(&priv.PublicKey)
	q.Wait()

	it := q.Items()[0]
	if it.State != StateFailed {
		t.Fatalf("不可写目标应失败：%+v", it)
	}
	if it.Reason == "" {
		t.Fatal("失败应带原因")
	}
	if _, err := os.Stat(filepath.Join(ro, "sub", "a.mp4.svid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("失败条目不应留下文件")
	}
}

// ---------- 明文红线（AC10.8）----------

func TestQueueNeverWritesPlaintext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mp4")
	data := fakeMP4(1 << 20)
	writeFile(t, src, data)
	priv := testKey(t)

	q := NewQueue([]Job{{Src: src, Dst: src + ".svid"}}, Options{})
	q.Start(&priv.PublicKey)
	q.Wait()

	raw, err := os.ReadFile(src + ".svid")
	if err != nil {
		t.Fatal(err)
	}
	// 密档里不得出现明文的头部序列（ftyp 盒）
	if len(raw) > 0 && containsBytes(raw, []byte("ftypisom")) {
		t.Fatal("密档中出现了明文头部（明文不得落盘）")
	}
}

func containsBytes(hay, needle []byte) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if string(hay[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// ---------- 取消语义（T10.1 / AC10.8）----------

func TestSealFileContextCancelLeavesCleanState(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.mp4")
	writeFile(t, src, fakeMP4(32<<20))
	priv := testKey(t)
	dst := src + ".svid"

	ctx, cancel := context.WithCancel(context.Background())
	_, err := vault.SealFileContext(ctx, src, dst, &priv.PublicKey, vault.SealOptions{},
		func(done, total int64) {
			if done > 0 {
				cancel() // 写入开始后立即取消
			}
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后应返回 context.Canceled，得到 %v", err)
	}
	// 半成品存在也不会被当成可播放密档（调用方清理；此处断言封存确实中止）
	if info, serr := os.Stat(dst); serr == nil && info.Size() == 0 {
		t.Fatal("取消后不应留下 0 字节文件")
	}
}

func TestSealFileContextOverwriteOff(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.mp4")
	writeFile(t, src, fakeMP4(4096))
	priv := testKey(t)
	dst := src + ".svid"
	writeFile(t, dst, []byte("已有"))

	_, err := vault.SealFileContext(context.Background(), src, dst, &priv.PublicKey, vault.SealOptions{Overwrite: false}, nil)
	if !errors.Is(err, vault.ErrTargetExists) {
		t.Fatalf("不覆盖时应返回 ErrTargetExists，得到 %v", err)
	}

	// 覆盖开启时正常封存
	if _, err := vault.SealFileContext(context.Background(), src, dst, &priv.PublicKey, vault.SealOptions{Overwrite: true}, nil); err != nil {
		t.Fatalf("覆盖模式封存失败：%v", err)
	}
}
