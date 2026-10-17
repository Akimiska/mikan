package server

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// OwnSite serves dir, a site of the admin's own (/opt/mikan/data/panel/www on the host), on
// every path the panel does not own. In front of a REALITY self-steal the panel is what a
// stranger sees, and the same bare 404 on "/", robots.txt and favicon.ico is a site with no
// page at all (GitHub issue #67). A missing file gets dir/404.html with status 404 when
// there is one. Without dir, or with nothing in it, the panel answers as before: NotFound.
func OwnSite(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root, err := os.OpenRoot(dir) // no way out of dir, symlinks included
		if err != nil {
			NotFound(w)
			return
		}
		defer root.Close()
		fsys := root.FS()
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if !servable(fsys, name) {
			if !servable(fsys, "404.html") {
				NotFound(w)
				return
			}
			plain(w.Header())
			page, err := fs.ReadFile(fsys, "404.html")
			if err != nil {
				NotFound(w)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(page)
			return
		}
		plain(w.Header())
		http.FileServerFS(fsys).ServeHTTP(w, r)
	})
}

// servable: a file, or a directory with index.html; never a hidden one (.git, .env).
func servable(fsys fs.FS, name string) bool {
	if name == "" {
		name = "."
	}
	for seg := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." {
			return false
		}
	}
	st, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}
	if st.IsDir() {
		st, err = fs.Stat(fsys, path.Join(name, "index.html"))
		return err == nil && !st.IsDir()
	}
	return true
}

// plain drops the panel's own policy headers: an ordinary site does not send them, and
// they would tell the panel apart.
func plain(h http.Header) {
	for _, k := range []string{"Content-Security-Policy", "Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy", "Permissions-Policy", "X-Frame-Options", "Referrer-Policy"} {
		h.Del(k)
	}
}
