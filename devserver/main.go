// Command devserver serves the translator's static files, setting the
// headers a browser needs before it grants a page SharedArrayBuffer —
// required for the multi-threaded build of yzma's WASM module. Without them
// yzma-loader.js falls back to the single-threaded build, which is slower
// but still works.
//
//	go run ./devserver
//	go run ./devserver -port 8090
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"
)

func main() {
	dir := flag.String("dir", "web", "directory of static files to serve")
	port := flag.String("port", "8090", "port to listen on")
	isolate := flag.Bool("isolate", true,
		"set COOP/COEP so the browser grants SharedArrayBuffer (multi-threaded WASM). "+
			"Run a second instance with -isolate=false -port 8091 to A/B the single-threaded "+
			"build against the multi-threaded one, e.g. if threads look like they are "+
			"contending on a CPU-constrained host (see README Troubleshooting).")
	flag.Parse()

	files := http.FileServer(http.Dir(*dir))

	var handler http.Handler = files
	if *isolate {
		handler = isolationHeaders(files)
	} else {
		handler = wasmContentType(files)
	}

	addr := ":" + *port
	log.Printf("serving %s at http://localhost%s (isolate=%v)", *dir, addr, *isolate)
	log.Fatal(http.ListenAndServe(addr, handler))
}

// isolationHeaders sets the headers a browser needs before it grants a page
// SharedArrayBuffer, and the content type a .wasm file needs to run.
func isolationHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		wasmContentType(next).ServeHTTP(w, r)
	})
}

// wasmContentType sets the content type a .wasm file needs to run, with no
// COOP/COEP — used by -isolate=false so yzma-loader.js falls back to the
// single-threaded build, for comparison against the multi-threaded one.
func wasmContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Ext(r.URL.Path) == ".wasm" {
			w.Header().Set("Content-Type", "application/wasm")
		}
		next.ServeHTTP(w, r)
	})
}
