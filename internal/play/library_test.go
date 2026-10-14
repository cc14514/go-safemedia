package play_test

import (
	"bytes"
	"crypto/ecdsa"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// sealVault 把 plain 封存到 dir/name，返回密档路径。
func sealVault(t *testing.T, dir, name string, priv *ecdsa.PrivateKey, plain []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Seal(bytes.NewReader(plain), f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, testChunk); err != nil {
		f.Close()
		t.Fatalf("封存失败: %v", err)
	}
	f.Close()
	return path
}

func TestScanLibraryFiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	priv := newKey(t)
	plain := fakeMP4(2000)
	sealVault(t, dir, "b.mp4.svid", priv, plain)
	sealVault(t, dir, "a.mp4.svid", priv, plain)
	// 干扰项：非 .svid、隐藏文件、子目录
	mustWrite(t, filepath.Join(dir, "note.txt"), []byte("hello"))
	mustWrite(t, filepath.Join(dir, ".hidden.svid"), []byte("not really"))
	if err := os.MkdirAll(filepath.Join(dir, "sub.svid"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := play.ScanLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("条目数 = %d, 期望 2（%v）", len(entries), entries)
	}
	if entries[0].Name != "a.mp4.svid" || entries[1].Name != "b.mp4.svid" {
		t.Fatalf("条目未按名称排序: %q, %q", entries[0].Name, entries[1].Name)
	}
	if entries[0].Size <= 0 {
		t.Fatalf("条目体积异常: %d", entries[0].Size)
	}
}

func TestLibraryIndexAndPlayback(t *testing.T) {
	dir := t.TempDir()
	priv := newKey(t)
	plainA := fakeMP4(5000)
	plainB := fakeMP4(3000)
	sealVault(t, dir, "clipA.mp4.svid", priv, plainA)
	sealVault(t, dir, "clipB.mp4.svid", priv, plainB)

	entries, err := play.ScanLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{Priv: priv, Entries: entries, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	ts := httptest.NewServer(lib.Handler())
	defer ts.Close()

	// 索引页：列出密档文件名；不含原始视频名（这里原始名即密档名去后缀，确保没有额外元数据）
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	index := string(body)
	for _, name := range []string{"clipA.mp4.svid", "clipB.mp4.svid"} {
		if !strings.Contains(index, name) {
			t.Fatalf("索引页缺少条目 %s:\n%s", name, index)
		}
	}
	if strings.Contains(index, "<video") {
		t.Fatal("索引页不应直接内嵌播放器")
	}
	if strings.Contains(index, "duration") || strings.Contains(index, "resolution") {
		t.Fatal("索引页不应包含时长/分辨率等元数据")
	}

	watchLink := regexp.MustCompile(`href="(/watch/[0-9a-f]+)"`).FindStringSubmatch(index)
	if watchLink == nil {
		t.Fatalf("索引页没有 watch 链接:\n%s", index)
	}

	// 播放页：含 <video> 与媒体地址
	wresp, err := http.Get(ts.URL + watchLink[1])
	if err != nil {
		t.Fatal(err)
	}
	wpage, _ := io.ReadAll(wresp.Body)
	wresp.Body.Close()
	media := regexp.MustCompile(`src="(/media/[0-9a-f]+/[0-9a-f]+)"`).FindStringSubmatch(string(wpage))
	if media == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", wpage)
	}

	// 全量拉流：必须与对应明文一致
	mresp, err := http.Get(ts.URL + media[1])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("媒体状态码 = %d", mresp.StatusCode)
	}
	if !bytes.Equal(got, plainA) {
		t.Fatal("目录模式拉流内容与明文不一致")
	}

	// Range 拖进度条
	req, _ := http.NewRequest(http.MethodGet, ts.URL+media[1], nil)
	req.Header.Set("Range", "bytes=1000-1999")
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rbody, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if rresp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 状态码 = %d", rresp.StatusCode)
	}
	if !bytes.Equal(rbody, plainA[1000:2000]) {
		t.Fatal("目录模式 Range 内容不一致")
	}

	// 未知 id → 404
	bad, err := http.Get(ts.URL + "/watch/0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusNotFound {
		t.Fatalf("未知 id 状态码 = %d, 期望 404", bad.StatusCode)
	}
}

func TestLibraryEmptyShowsHint(t *testing.T) {
	dir := t.TempDir()
	priv := newKey(t)
	entries, err := play.ScanLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("空目录应无条目，得到 %d", len(entries))
	}
	lib, err := play.NewLibrary(play.LibraryOptions{Priv: priv, Entries: entries, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	ts := httptest.NewServer(lib.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "没有 .svid 密档") {
		t.Fatalf("空列表缺提示:\n%s", body)
	}
}

func TestLibraryWrongAccountForbidden(t *testing.T) {
	dir := t.TempDir()
	owner := newKey(t)
	other := newKey(t)
	sealVault(t, dir, "mine.mp4.svid", owner, fakeMP4(1200))

	entries, err := play.ScanLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{Priv: other, Entries: entries, IdleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	ts := httptest.NewServer(lib.Handler())
	defer ts.Close()

	// 直接猜媒体地址（token 未知，先取索引→watch 拿到真实链接）
	idx, _ := http.Get(ts.URL + "/")
	page, _ := io.ReadAll(idx.Body)
	idx.Body.Close()
	link := regexp.MustCompile(`href="(/watch/[0-9a-f]+)"`).FindStringSubmatch(string(page))
	if link == nil {
		t.Fatalf("索引页没有条目: %s", page)
	}
	wp, _ := http.Get(ts.URL + link[1])
	wb, _ := io.ReadAll(wp.Body)
	wp.Body.Close()
	media := regexp.MustCompile(`src="(/media/[0-9a-f]+/[0-9a-f]+)"`).FindStringSubmatch(string(wb))
	if media == nil {
		t.Fatalf("播放页没有媒体地址: %s", wb)
	}
	resp, err := http.Get(ts.URL + media[1])
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("非本账户密档状态码 = %d, 期望 403", resp.StatusCode)
	}
}

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
