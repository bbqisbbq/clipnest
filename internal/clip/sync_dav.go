package clip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
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

