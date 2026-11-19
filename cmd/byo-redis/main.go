// Command byo-redis запускает Redis-совместимый сервер.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Oleg4311/byo-redis-go/internal/aof"
	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/server"
)

var version = engine.Version

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "адрес для прослушивания")
	aofPath := flag.String("aof", "", "путь к журналу AOF (пусто: данные только в памяти)")
	fsync := flag.String("appendfsync", "everysec", "как часто сбрасывать журнал на диск: always, everysec, no")
	showVersion := flag.Bool("version", false, "показать версию")
	flag.Parse()
	if *showVersion {
		fmt.Println("byo-redis-go", version)
		return
	}
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("byo-redis: ")

	eng := engine.New()
	var journal *aof.Log
	if *aofPath != "" {
		policy, err := aof.ParsePolicy(*fsync)
		if err != nil {
			log.Fatal(err)
		}
		journal, err = aof.Open(*aofPath, policy)
		if err != nil {
			log.Fatalf("не удалось открыть журнал: %v", err)
		}
		n, truncated, err := journal.Replay(func(args [][]byte) { eng.Replay(args) })
		if err != nil {
			log.Fatalf("восстановление из журнала: %v", err)
		}
		if truncated {
			log.Printf("в конце журнала найдена оборванная команда, она отброшена")
		}
		log.Printf("журнал %s: восстановлено команд: %d", *aofPath, n)
		eng.OnWrite(func(args [][]byte) {
			if err := journal.Append(args); err != nil {
				log.Printf("ошибка записи в журнал: %v", err)
			}
		})
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := server.New(eng, log.Printf)
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Printf("останавливаюсь…")
		srv.Close()
	}()

	log.Printf("версия %s, слушаю %s", version, ln.Addr())
	if err := srv.Serve(ln); err != nil {
		log.Fatal(err)
	}
	if journal != nil {
		if err := journal.Close(); err != nil {
			log.Printf("закрытие журнала: %v", err)
		}
	}
	log.Printf("остановлен")
}
