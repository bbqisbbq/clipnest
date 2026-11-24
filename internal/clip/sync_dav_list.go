package clip

// WebDAV 的**目录发现**：PROPFIND 解析、列包、找兄弟目录。
//
// 单独一个文件的理由：这一块是"说 WebDAV 方言"（XML 命名空间、深度、路径归一化、
// 服务器之间的怪癖），与"怎么传字节"（sync_dav.go）和"包里装什么"（sync_package.go）
// 是三件不同的事。PROPFIND 的坑见 HISTORY「PROPFIND 的 props 要自己列」。

import (
	"encoding/xml"
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
