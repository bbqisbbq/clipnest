// Command fakeupdate serves a minimal GitHub "latest release" response so the
// update banner can be exercised without touching the real API.
//
//	go run ./scratch/fakeupdate -addr 127.0.0.1:8901 -version 9.9.9
//	set CLIPNEST_UPDATE_API=http://127.0.0.1:8901
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8901", "listen address")
	version := flag.String("version", "9.9.9", "version to advertise")
	flag.Parse()

	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/bbqisbbq/clipnest/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "tag_name": "v%s",
  "name": "ClipNest %s",
  "body": "- 假的发布说明，仅用于本地验证更新提示\n- 第二条",
  "published_at": "2026-11-17T07:00:00Z",
  "assets": [
    {"name":"clipnest-%s-amd64-setup.exe","size":13037398,"browser_download_url":"%s/dl/setup.exe"}
  ]
}`, *version, *version, *version, base)
	})
	mux.HandleFunc("/dl/setup.exe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(make([]byte, 1024))
	})

	base = "http://" + *addr
	log.Printf("fake update API on %s (advertising v%s)", base, *version)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
