package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type api struct {
	ctx     context.Context
	mu      sync.Mutex
	memory  [][]byte
	heldMiB uint64
	burning atomic.Bool
}

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /eat", a.eat)
	mux.HandleFunc("GET /burn", a.burn)
	return mux
}

func (a *api) eat(w http.ResponseWriter, r *http.Request) {
	mb, err := strconv.ParseUint(r.URL.Query().Get("mb"), 10, 64)
	maxInt := uint64(^uint(0) >> 1)
	if err != nil || mb == 0 || mb > maxInt/(1<<20) {
		http.Error(w, "mb must be a positive integer fitting in the address space", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	log.Printf("allocating %d MiB", mb)
	block := make([]byte, int(mb)*(1<<20))
	// Записываем в каждую страницу, чтобы память была физически затронута,
	// а не только зарезервирована в виртуальном адресном пространстве.
	for i := 0; i < len(block); i += os.Getpagesize() {
		block[i] = 1
	}
	// Ссылка удерживает память от сборки GC до завершения процесса.
	a.memory = append(a.memory, block)
	a.heldMiB += mb
	fmt.Fprintf(w, "allocated_mib=%d held_mib=%d\n", mb, a.heldMiB)
}

func (a *api) burn(w http.ResponseWriter, r *http.Request) {
	if !a.burning.CompareAndSwap(false, true) {
		fmt.Fprintln(w, "CPU burner already running")
		return
	}
	// Одна вычисляющая goroutine потребляет примерно одно ядро CPU.
	// Контекст принадлежит сервису: закрытие HTTP-запроса не снимает нагрузку.
	go func() {
		defer a.burning.Store(false)
		var counter uint64
		for {
			counter++
			if counter%65536 == 0 {
				select {
				case <-a.ctx.Done():
					return
				default:
				}
			}
		}
	}()
	log.Print("CPU burner started")
	fmt.Fprintln(w, "CPU burner started")
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &api{ctx: ctx}
	server := &http.Server{Addr: addr, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("api listening on %s, pid=%d", addr, os.Getpid())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-stopped
}
