package clip

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WebDAV 传输层：syncConfig（地址/账号/设备 id）以及 GET / PUT / MKCOL / PROPFIND。
// 「一台机器一个包」的路径命名也在这里，因为文件名本身就是远端协议的一部分。
// 不负责：包的内容（sync_package.go）、合并语义（sync_merge.go）、何时同步（sync.go）。

// ---- transport ----------------------------------------------------------

type syncConfig struct {
	base     string
	user     string
	pass     string
	deviceID string
	// ctx 来自 ClipService.bgCtx：退出时取消，在途的 HTTP 请求立刻返回，
	// Shutdown 才等得到后台循环。
	ctx context.Context
}

// ctxWithTimeout bounds one request, and inherits the service lifetime.
func (c syncConfig) ctxWithTimeout() (context.Context, context.CancelFunc) {
	parent := c.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, syncHTTPTimeout)
}

func (s *ClipService) syncConfig() (syncConfig, error) {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncURL) == "" {
		return syncConfig{}, errors.New("还没有填写 WebDAV 地址")
	}
	deviceID := strings.TrimSpace(st.SyncDeviceID)
	if deviceID == "" {
		deviceID = s.ensureDeviceID()
	}
	return syncConfig{
		base:     strings.TrimRight(strings.TrimSpace(st.SyncURL), "/") + "/",
		user:     st.SyncUser,
		pass:     st.SyncPass,
		deviceID: deviceID,
		ctx:      s.bgCtx,
	}, nil
}

// ensureDeviceID gives this installation a stable random identity. Hostnames are
// not unique enough (cloned VMs, renamed machines) and two devices sharing an id
// would overwrite each other's package.
func (s *ClipService) ensureDeviceID() string {
	st := s.store.Settings()
	if strings.TrimSpace(st.SyncDeviceID) != "" {
		return st.SyncDeviceID
	}
	st.SyncDeviceID = newID()
	if err := s.store.SaveSettings(st); err != nil {
		Logf(s.store.Dir(), "sync: could not persist device id: %v", err)
	}
	return st.SyncDeviceID
}

// packageName is this machine's file inside syncDevicesDir. The hostname is part
// of the name on purpose: a user who copies %APPDATA\%ClipNest to another PC
// would otherwise clone the device id and the two machines would overwrite the
// same package.
func (c syncConfig) packageName() string {
	host := slug(hostName())
	if host == "" {
		return c.deviceID + ".zip"
	}
	return c.deviceID + "-" + host + ".zip"
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func (c syncConfig) urlFor(rel string) (string, error) {
	u, err := url.Parse(c.base)
	if err != nil {
		return "", fmt.Errorf("WebDAV 地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("WebDAV 地址必须以 http:// 或 https:// 开头")
	}
	u.Path = path.Join(u.Path, rel)
	return u.String(), nil
}

// urlForDir is urlFor with a guaranteed trailing slash. PROPFIND 的目标必须是
// 目录形态的 URL：不带斜杠时不少服务器（含坚果云）会 301 到带斜杠的地址，
// 而 Go 的 http.Client 对 301 + PROPFIND 的处理是把它降级成 GET。
func (c syncConfig) urlForDir(rel string) (string, error) {
	s, err := c.urlFor(rel)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(s, "/") {
		s += "/"
	}
	return s, nil
}

func newSyncHTTPClient() *http.Client { return &http.Client{Timeout: syncHTTPTimeout} }

func (c syncConfig) newRequest(ctx context.Context, method, rel string, body io.Reader) (*http.Request, error) {
	target, err := c.urlFor(rel)
	if err != nil {
		return nil, err
	}
	return c.newRequestFor(ctx, method, target, body)
}

// newRequestFor builds a request against an already-resolved absolute URL.
func (c syncConfig) newRequestFor(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)
	return req, nil
}

// davGet returns (body, true) or (nil, false) when the file does not exist.
func (c syncConfig) davGet(rel string) ([]byte, bool, error) {
	ctx, cancel := c.ctxWithTimeout()
	defer cancel()

	req, err := c.newRequest(ctx, http.MethodGet, rel, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("连接失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, false, nil
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return nil, false, fmt.Errorf("认证失败（HTTP %d），请检查用户名/密码", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, false, fmt.Errorf("读取 %s 失败（HTTP %d）: %s",
			rel, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (c syncConfig) davPut(rel, contentType string, payload []byte) error {
	put := func() (*http.Response, error) {
		ctx, cancel := c.ctxWithTimeout()
		defer cancel()
		req, err := c.newRequest(ctx, http.MethodPut, rel, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.ContentLength = int64(len(payload))
		return newSyncHTTPClient().Do(req)
	}

	resp, err := put()
	if err != nil {
		return fmt.Errorf("上传失败: %w", err)
	}
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		if err := c.mkcol(path.Dir(rel)); err != nil {
			return err
		}
		if resp, err = put(); err != nil {
			return fmt.Errorf("上传失败: %w", err)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("上传 %s 失败（HTTP %d）: %s", rel, resp.StatusCode, strings.TrimSpace(string(body)))
}

// mkcol creates one directory level (and its parent when needed).
func (c syncConfig) mkcol(dir string) error {
	return c.mkcolDepth(dir, 0)
}

// mkcolDepth carries a depth guard: 409 表示"上级目录不存在"，正常做法是先把上级建出来
// 再重试自己。但 `clipnest_data` 的上级是 `.`，而 `.` 会直接命中上面的早退分支返回 nil，
// 于是"建上级成功 → 再建自己 → 又是 409"变成死循环（服务器回 409 而不是 405 时会栈溢出）。
// 现在：没有上级可建时直接报错，层级也设上限。
func (c syncConfig) mkcolDepth(dir string, depth int) error {
	const maxDepth = 8
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	if depth > maxDepth {
		return fmt.Errorf("创建目录 %s 失败：层级太深", dir)
	}
	ctx, cancel := c.ctxWithTimeout()
	defer cancel()
	req, err := c.newRequest(ctx, "MKCOL", dir+"/", nil)
	if err != nil {
		return err
	}
	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusMethodNotAllowed, http.StatusCreated, http.StatusOK, http.StatusNoContent:
		return nil // 405 = already there on most servers
	case http.StatusConflict:
		if parent := path.Dir(strings.TrimSuffix(dir, "/")); parent != dir && parent != "." && parent != "/" {
			if err := c.mkcolDepth(parent, depth+1); err != nil {
				return err
			}
			return c.mkcolDepth(dir, depth+1)
		}
		return fmt.Errorf("创建目录 %s 失败（HTTP 409）：上级目录不存在或没有创建权限", dir)
	}
	return fmt.Errorf("创建目录 %s 失败（HTTP %d）", dir, resp.StatusCode)
}

// ---- directory listing --------------------------------------------------

// davEntry is one child of a remote directory, as reported by PROPFIND.
type davEntry struct {
	Name         string
	Bytes        int64
	Modified     time.Time
	ETag         string
	IsCollection bool
}

// propfind lists the direct children of one remote directory (Depth: 1).
//
// 请求体里必须带上 getcontentlength / getlastmodified：只问 resourcetype 时，
// 坚果云（以及不少服务器）就**只**回 resourcetype，于是设置页设备表变成
// "包大小 0 B / 最后更新 —" —— 而这两个数字正是排查「另一台机器到底上没上传」
// 最需要的东西（docs/SYNC.md 的排查清单第一条就靠它）。getetag 顺手拿到，
// 以后要"对方包没变就不 GET"就不必再改协议。
func (c syncConfig) propfind(rel string) ([]davEntry, error) {
	ctx, cancel := c.ctxWithTimeout()
	defer cancel()

	target, err := c.urlForDir(rel)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequestFor(ctx, "PROPFIND", target, strings.NewReader(
		`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop>`+
			`<resourcetype/><getcontentlength/><getlastmodified/><getetag/>`+
			`</prop></propfind>`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")

	resp, err := newSyncHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("列目录失败: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return nil, nil
	case http.StatusMultiStatus, http.StatusOK:
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("列目录失败（HTTP %d）: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var ms struct {
		Responses []struct {
			Href     string `xml:"href"`
			Propstat []struct {
				Prop struct {
					ContentLength string `xml:"getcontentlength"`
					LastModified  string `xml:"getlastmodified"`
					ETag          string `xml:"getetag"`
					ResourceType  struct {
						Collection *struct{} `xml:"collection"`
					} `xml:"resourcetype"`
				} `xml:"prop"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, fmt.Errorf("目录列表解析失败: %w", err)
	}

	// 先算出每一项的 href（解码后、去掉尾斜杠），再把**最短的那一条**认成这个目录
	// 自己：Depth 1 的响应里目录始终是路径最短的那一项，而服务器回的是
	// 去掉 Prefix 后的相对 href（`/clipnest_data/`），跟请求 URL 对不上，比长度最稳。
	type parsed struct {
		href  string
		entry davEntry
	}
	items := make([]parsed, 0, len(ms.Responses))
	shortest := -1
	for _, r := range ms.Responses {
		href := r.Href
		if unescaped, err := url.PathUnescape(href); err == nil {
			href = unescaped
		}
		href = strings.TrimSuffix(href, "/")
		// 目录的 href 在有些服务器上会带上与请求 URL 不同的前缀，只取最后两段路径会
		// 丢信息，所以这里保留完整 href 做比较，名字单独取 base。
		e := davEntry{Name: path.Base(href)}
		if strings.HasSuffix(r.Href, "/") {
			e.IsCollection = true
		}
		for _, ps := range r.Propstat {
			if ps.Prop.ResourceType.Collection != nil {
				e.IsCollection = true
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLength), 10, 64); err == nil {
				e.Bytes = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(ps.Prop.LastModified)); err == nil {
				e.Modified = t
			}
			if tag := strings.TrimSpace(ps.Prop.ETag); tag != "" {
				e.ETag = tag
			}
		}
		if shortest < 0 || len(href) < shortest {
			shortest = len(href)
		}
		items = append(items, parsed{href: href, entry: e})
	}

	out := make([]davEntry, 0, len(items))
	for _, it := range items {
		if it.entry.IsCollection && len(it.href) == shortest {
			continue // 这个目录自己
		}
		out = append(out, it.entry)
	}
	return out, nil
}

// listPackages returns this device directory's packages, plus whatever else it
// holds. 无法识别的文件（旧版本布局、半截上传、`.bad`）要报给用户看：静默忽略
// 会让"另一台机器在同步、这台只看到自己的包"这种局面没有任何线索。
func (c syncConfig) listPackages(dir string) (pkgs []davEntry, ignored []string, err error) {
	entries, err := c.propfind(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsCollection {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name), ".zip") {
			ignored = append(ignored, e.Name)
			continue
		}
		pkgs = append(pkgs, e)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	return pkgs, ignored, nil
}

// listCollections returns the names of the child collections of rel.
func (c syncConfig) listCollections(rel string) ([]string, error) {
	entries, err := c.propfind(rel)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsCollection && e.Name != "" && e.Name != "." {
			out = append(out, e.Name)
		}
	}
	return out, nil
}

// siblingDataDirs looks for other `clipnest_data` folders next to ours.
//
// 两台机器只要「服务器地址」差一层（`…/dav` vs `…/dav/我的坚果云`），就会各自
// 建一个 clipnest_data、各写各的包：远端永远只有"本机一个包"，而两边日志都是
// "sync ok"。这是这类问题的头号原因，但一点报错都没有。这里做一次只读 PROPFIND
// 把邻居找出来（最多探 8 个同级目录，找到 4 个就停）。
func (c syncConfig) siblingDataDirs() []string {
	parent := path.Dir(syncDevicesDir)
	names, err := c.listCollections(parent)
	if err != nil {
		return nil
	}
	var out []string
	checked := 0
	for _, name := range names {
		if name == "" || name == syncDevicesDir {
			continue
		}
		if checked >= 8 || len(out) >= 4 {
			break
		}
		checked++
		children, err := c.listCollections(path.Join(parent, name))
		if err != nil {
			continue
		}
		for _, ch := range children {
			if ch == syncDevicesDir {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// siblingHint is the one-sentence explanation shown in the status line, the
// panel and TestSync when neighbours exist ("" when there are none).
func (c syncConfig) siblingHint() string {
	dirs := c.siblingDataDirs()
	if len(dirs) == 0 {
		return ""
	}
	urls := make([]string, 0, len(dirs))
	for _, d := range dirs {
		urls = append(urls, c.base+d)
	}
	return fmt.Sprintf("同账号下 %s 里也有 clipnest_data 同步包 —— 另一台机器的「服务器地址」很可能填成了 %s；两边地址必须一字不差（多一层目录就是两个互不相干的仓库）",
		strings.Join(urls, "、"), c.base+dirs[0])
}
