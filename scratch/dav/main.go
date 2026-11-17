//go:build windows

// dav is a throwaway WebDAV server used to exercise ClipNest's sync path
// against a real HTTP endpoint:  go run ./scratch/dav -addr 127.0.0.1:8899 -dir <dir>
package main

import (
	"flag"
	"log"
	"net/http"

	"golang.org/x/net/webdav"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8899", "listen address")
	dir := flag.String("dir", "./scratch-dav", "directory to serve")
	user := flag.String("user", "", "basic auth user (optional)")
	pass := flag.String("pass", "", "basic auth password (optional)")
	flag.Parse()

	handler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: webdav.Dir(*dir),
		LockSystem: webdav.NewMemLS(),
	}

	var h http.Handler = handler
	if *user != "" {
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok || u != *user || p != *pass {
				w.Header().Set("WWW-Authenticate", `Basic realm="clipnest"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			handler.ServeHTTP(w, r)
		})
	}

	log.Printf("webdav on http://%s serving %s", *addr, *dir)
	if err := http.ListenAndServe(*addr, h); err != nil {
		log.Fatal(err)
	}
}
