// Package server принимает TCP-соединения, разбирает RESP и передаёт команды движку.
package server

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// Server обслуживает клиентов одного движка.
type Server struct {
	eng  *engine.Engine
	logf func(format string, args ...any)

	mu     sync.Mutex
	ln     net.Listener
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
	stop   chan struct{}
}

// New создаёт сервер. logf может быть nil.
func New(eng *engine.Engine, logf func(string, ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Server{eng: eng, logf: logf, conns: map[net.Conn]struct{}{}, stop: make(chan struct{})}
}

// Serve принимает соединения, пока слушатель не закрыт. Возвращает nil после Close.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.ln = ln
	s.mu.Unlock()

	s.wg.Add(1)
	go s.sweeper()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return nil
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.handle(conn)
	}
}

// Close останавливает приём, закрывает соединения и ждёт завершения обработчиков.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stop)
	if s.ln != nil {
		s.ln.Close()
	}
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// sweeper — «активное» удаление просроченных ключей по схеме Redis: сэмпл из 20 ключей каждые 100 мс,
// и если просрочено хотя бы четверть, сразу берём следующий сэмпл.
func (s *Server) sweeper() {
	defer s.wg.Done()
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			for i := 0; i < 16 && s.eng.SweepExpired(20) >= 5; i++ {
			}
		case <-s.stop:
			return
		}
	}
}

func (s *Server) handle(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	rd := resp.NewReader(conn)
	bw := bufio.NewWriterSize(conn, 16<<10)
	sess := &engine.Session{}
	for {
		args, err := rd.ReadCommand()
		if err != nil {
			var pe *resp.ProtocolError
			if errors.As(err, &pe) {
				_ = resp.Encode(bw, resp.Err("ERR "+pe.Error()))
				_ = bw.Flush()
				s.logf("клиент %s: %v", conn.RemoteAddr(), pe)
			} else if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrUnexpectedEOF) {
				s.logf("клиент %s: %v", conn.RemoteAddr(), err)
			}
			return
		}
		if err := resp.Encode(bw, s.eng.Execute(sess, args)); err != nil {
			return
		}
		// Pipelining: ответы копятся в буфере, пока во входящем потоке есть неразобранные команды.
		if rd.Buffered() == 0 || sess.Close {
			if err := bw.Flush(); err != nil {
				return
			}
		}
		if sess.Close {
			return
		}
	}
}
