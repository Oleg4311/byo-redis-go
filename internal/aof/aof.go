// Package aof — журнал только на добавление (append-only file). Формат тот же, что у Redis:
// каждая команда записи сохраняется как RESP-массив, а восстановление — это повторное выполнение журнала.
package aof

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// Policy задаёт, как часто журнал сбрасывается на диск (fsync).
type Policy string

const (
	Always   Policy = "always"   // после каждой команды: надёжно, но медленно
	EverySec Policy = "everysec" // раз в секунду: можно потерять до секунды записей
	No       Policy = "no"       // полагаемся на ОС
)

// ParsePolicy проверяет строку из флага.
func ParsePolicy(s string) (Policy, error) {
	switch p := Policy(s); p {
	case Always, EverySec, No:
		return p, nil
	}
	return "", fmt.Errorf("неизвестная политика %q: допустимо always, everysec, no", s)
}

// Log — открытый файл журнала.
type Log struct {
	mu     sync.Mutex
	f      *os.File
	policy Policy
	dirty  bool
	stop   chan struct{}
	done   chan struct{}
}

// Open открывает (или создаёт) журнал. Перед записью нужно вызвать Replay.
func Open(path string, policy Policy) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f, policy: policy, stop: make(chan struct{}), done: make(chan struct{})}
	if policy == EverySec {
		go l.syncLoop()
	} else {
		close(l.done)
	}
	return l, nil
}

func (l *Log) syncLoop() {
	defer close(l.done)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.mu.Lock()
			if l.dirty {
				_ = l.f.Sync()
				l.dirty = false
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// ErrCorrupt означает повреждение журнала не в конце файла: молча отбрасывать данные нельзя.
var ErrCorrupt = errors.New("журнал повреждён")

// Replay применяет все команды журнала. Оборванная последняя команда (сбой питания посреди записи)
// отбрасывается, а файл усекается до последней целой команды. Повреждение посреди файла — ошибка.
func (l *Log) Replay(apply func(args [][]byte)) (n int, truncated bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Seek(0, io.SeekStart); err != nil {
		return 0, false, err
	}
	cr := &countReader{r: l.f}
	rd := resp.NewReader(cr)
	var good int64
	for {
		args, rerr := rd.ReadCommand()
		if rerr == io.EOF {
			break
		}
		if errors.Is(rerr, io.ErrUnexpectedEOF) {
			truncated = true
			break
		}
		if rerr != nil {
			return n, false, fmt.Errorf("%w: смещение %d: %v", ErrCorrupt, good, rerr)
		}
		apply(args)
		n++
		good = cr.n - int64(rd.Buffered())
	}
	if truncated {
		if err := l.f.Truncate(good); err != nil {
			return n, true, err
		}
	}
	_, err = l.f.Seek(good, io.SeekStart)
	return n, truncated, err
}

// Append дописывает команду в конец журнала.
func (l *Log) Append(args [][]byte) error {
	data := resp.Marshal(resp.BulkArray(args))
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(data); err != nil {
		return err
	}
	switch l.policy {
	case Always:
		return l.f.Sync()
	case EverySec:
		l.dirty = true
	}
	return nil
}

// Close останавливает фоновую синхронизацию, сбрасывает данные на диск и закрывает файл.
func (l *Log) Close() error {
	select {
	case <-l.stop:
	default:
		close(l.stop)
	}
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.f.Sync(); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}
