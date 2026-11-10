// Provedor falso para os testes de navegador: go run ./provedorfalso/cmd :8097
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

func main() {
	addr := ":8097"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	p := &provedorfalso.Provedor{}
	log.Printf("provedor falso em %s", addr)
	log.Fatal(http.ListenAndServe(addr, p.Handler()))
}
