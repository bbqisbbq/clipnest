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

func newSyncHTTPClient() *http.Client { return &http.Client{Timeout: syncHTTPTimeout} }

func (c syncConfig) newRequest(ctx context.Context, method, rel string, body io.Reader) (*http.Request, error) {
	target, err := c.urlFor(rel)
	if err != nil {
		return nil, err
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
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
		ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
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
	if dir == "." || dir == "/" || dir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
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
		if err := c.mkcol(path.Dir(dir)); err != nil {
			return err
		}
		return c.mkcol(dir)
	}
	return fmt.Errorf("创建目录 %s 失败（HTTP %d）", dir, resp.StatusCode)
}

// listPackages returns the package names (without directory) inside dir.
// davEntry is one file in the remote directory, as reported by PROPFIND.
type davEntry struct {
	Name     string
	Bytes    int64
	Modified time.Time
}

func (c syncConfig) listPackages(dir string) ([]davEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncHTTPTimeout)
	defer cancel()
	req, err := c.newRequest(ctx, "PROPFIND", dir+"/", strings.NewReader(
		`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
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

	out := make([]davEntry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		name := path.Base(strings.TrimSuffix(r.Href, "/"))
		if unescaped, err := url.PathUnescape(name); err == nil {
			name = unescaped
		}
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			continue
		}
		e := davEntry{Name: name}
		for _, ps := range r.Propstat {
			if n, err := strconv.ParseInt(strings.TrimSpace(ps.Prop.ContentLength), 10, 64); err == nil {
				e.Bytes = n
			}
			if t, err := http.ParseTime(strings.TrimSpace(ps.Prop.LastModified)); err == nil {
				e.Modified = t
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
