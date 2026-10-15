package integration

import (
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/seal"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// newRealSealer 是外壳里 newSealer() 的同款接线：把任务交给 internal/seal 的队列。
// postJSON 把一个 JSON 数组投递到指定地址（外壳拖放的同款请求）。
func postJSON(t *testing.T, url string, body any) httpResult {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResult{code: resp.StatusCode, body: string(b), bytes: b, header: resp.Header}
}

func newRealSealer() play.SealFactory {
	return func(jobs []seal.Job, pub *ecdsa.PublicKey) play.SealQueue {
		if len(jobs) == 0 || pub == nil {
			return nil
		}
		q := seal.NewQueue(jobs, seal.Options{RemovePartialOnFailure: true})
		q.Start(pub)
		return q
	}
}

// T10.9（AC10.1–AC10.5、AC10.10）：拖入视频 → 面板进度 → 库根出现密档 → 能启封 → 索引页可见。
//
// 这是「外壳拖放」的进程内等价链路：原生拖放只负责把路径 POST 到随机投递地址，
// 其余全在引擎里（所以这条用例覆盖了拖放之后的全部行为）。
func TestM10DragDropSealsAndShowsProgress(t *testing.T) {
	home := t.TempDir()
	const pw = "drop passphrase"
	keystore := newAccountForTest(t, home, pw)

	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 待封存的视频（用真实队列，所以给一块足够观察进度的体量）
	src := filepath.Join(home, "clip.mp4")
	plain := fakeMP4(3 << 20)
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	// 非白名单文件：必须逐条给出原因，而不是静默丢弃（AC10.4）
	txt := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatalf("加载账户失败: %v", err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Target:      libDir,
		Root:        libDir,
		Priv:        acct.PrivateKey(),
		IdleTimeout: play.DefaultIdleTimeout,
		Sealer:      newRealSealer(),
	})
	if err != nil {
		t.Fatalf("构造库失败: %v", err)
	}
	base, err := lib.Start()
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() { _ = lib.Close() }()

	// 未解锁就投递：拒绝且不产生文件（AC10.7）——这里库是预解锁的，先锁一次再解锁
	// 覆盖「锁定态投递」的正规入口由 L1 用例承担；此处只验证投递与封存链路。

	// 投递（外壳的原生拖放等价动作）
	resp := postJSON(t, lib.DropURL(), []string{src, txt})
	if resp.code != http.StatusOK {
		t.Fatalf("投递失败: %d %s", resp.code, resp.body)
	}
	var accepted struct{ Jobs, Skipped, Rejects int }
	if err := json.Unmarshal([]byte(resp.body), &accepted); err != nil {
		t.Fatalf("投递响应不是 JSON: %v（%s）", err, resp.body)
	}
	if accepted.Jobs != 1 {
		t.Fatalf("应受理 1 个视频: %+v", accepted)
	}

	// 面板首屏就有进度（AC10.5）
	panel := get(t, http.DefaultClient, base+"seal")
	if panel.code != http.StatusOK {
		t.Fatalf("封存面板不可达: %d", panel.code)
	}
	if !strings.Contains(panel.body, "clip.mp4") {
		t.Fatalf("面板应列出正在封存的文件:\n%s", panel.body)
	}
	if !strings.Contains(panel.body, "notes.txt") {
		t.Fatalf("面板应逐条给出未封存原因:\n%s", panel.body)
	}
	if !strings.Contains(panel.body, "封存") {
		t.Fatal("面板应显示封存进度")
	}

	// 轮询直到队列结束（面板自带轮询，这里等价地轮询 /seal/status）
	deadline := time.Now().Add(60 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		raw := get(t, http.DefaultClient, base+"seal/status")
		if err := json.Unmarshal([]byte(raw.body), &st); err != nil {
			t.Fatalf("状态不是 JSON: %v（%s）", err, raw.body)
		}
		if st["active"] == false {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["active"] != false {
		t.Fatal("封存队列未在 60s 内结束")
	}
	items, _ := st["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("状态应含 1 个条目: %+v", st)
	}
	first, _ := items[0].(map[string]any)
	if first["state"] != "完成" {
		t.Fatalf("文件应封存完成: %+v", first)
	}
	if first["percent"] != float64(100) {
		t.Fatalf("进度应到 100%%: %+v", first)
	}

	// 密档落在库根，且能用账户启封还原原文
	dst := filepath.Join(libDir, "clip.mp4.svid")
	if !fileExists(dst) {
		t.Fatalf("库根下未生成密档: %s", dst)
	}
	r, err := vault.Open(dst, acct.PrivateKey())
	if err != nil {
		t.Fatalf("Open 密档失败: %v", err)
	}
	defer r.Close()
	got := make([]byte, r.Size())
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt 失败: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatal("启封内容与原文不一致")
	}

	// 封存结束后重扫库根：新密档出现在索引页，无需重启（AC10.10）
	idx := get(t, http.DefaultClient, base+"/")
	if !strings.Contains(idx.body, "clip.mp4.svid") {
		t.Fatalf("封存结束后索引页应能看到新密档:\n%s", idx.body)
	}
}

// T10.10（AC10.6）：投递地址的 token 不对 → 404，避免本机其它页面盲投。
func TestM10DropTokenIsRequired(t *testing.T) {
	home := t.TempDir()
	const pw = "drop token passphrase"
	keystore := newAccountForTest(t, home, pw)
	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Target: libDir, Root: libDir, Priv: acct.PrivateKey(),
		IdleTimeout: play.DefaultIdleTimeout, Sealer: newRealSealer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lib.Close() }()

	bad := strings.TrimSuffix(lib.DropURL(), "/") + "deadbeef/"
	resp := postJSON(t, bad, []string{"/tmp/nonexistent.mp4"})
	if resp.code != http.StatusNotFound {
		t.Fatalf("错误 token 应 404，得到 %d（%s）", resp.code, resp.body)
	}
}
