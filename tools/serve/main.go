// serve: minimal static file server for local app testing (TELA hosts serve exactly like this: plain files on
// localhost, no headers of note). Usage: go run ./serve -dir ../app -addr 127.0.0.1:8082
package main

import (
	"flag"
	"fmt"
	"net/http"
)

func main() {
	dir := flag.String("dir", "../app", "directory to serve")
	addr := flag.String("addr", "127.0.0.1:8082", "listen address")
	flag.Parse()
	fmt.Printf("serving %s at http://%s/index.html\n", *dir, *addr)
	if err := http.ListenAndServe(*addr, http.FileServer(http.Dir(*dir))); err != nil {
		panic(err)
	}
}
