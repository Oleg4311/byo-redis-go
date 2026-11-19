// Package engine — сердце сервера: хранилище, таблица команд и транзакции.
// Здесь нет сети и файлов, поэтому один и тот же код работает в TCP-сервере, в тестах и в WASM-демо.
package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// Version — версия движка, её показывают INFO и HELLO.
const Version = "0.1.0"

const (
	errSyntax    = "ERR syntax error"
	errNotInt    = "ERR value is not an integer or out of range"
	errWrongType = "WRONGTYPE Operation against a key holding the wrong kind of value"
	errOverflow  = "ERR increment or decrement would overflow"
)

// Session — состояние одного клиента: транзакция и признак закрытия соединения.
type Session struct {
	inMulti bool
	dirty   bool
	queue   [][][]byte
	// Close выставляется командой QUIT: сервер закрывает соединение после ответа.
	Close bool
}

// Engine выполняет команды. Все команды исполняются под одним мьютексом, поэтому каждая из них,
// как и EXEC целиком, атомарна, это то же свойство, что даёт однопоточность настоящего Redis.
type Engine struct {
	mu       sync.Mutex
	db       *db
	now      func() time.Time
	onWrite  func(args [][]byte)
	replay   bool
	started  time.Time
	commands uint64
}

// Option настраивает Engine.
type Option func(*Engine)

// WithClock подменяет источник времени (для тестов).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// New создаёт пустой движок.
func New(opts ...Option) *Engine {
	e := &Engine{db: newDB(), now: time.Now}
	for _, o := range opts {
		o(e)
	}
	e.started = e.now()
	return e
}

// OnWrite задаёт обработчик успешных команд записи; он вызывается под блокировкой, в порядке выполнения.
// В него приходят аргументы в «переносимом» виде: сроки жизни уже превращены в абсолютное время.
func (e *Engine) OnWrite(fn func(args [][]byte)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onWrite = fn
}

// Replay выполняет команду из журнала: без записи обратно в журнал и без ответа.
func (e *Engine) Replay(args [][]byte) resp.Value {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.replay = true
	defer func() { e.replay = false }()
	return e.run(&Session{}, args)
}

// SweepExpired удаляет до max просроченных ключей. Возвращает число удалённых.
func (e *Engine) SweepExpired(max int) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.db.sweep(e.nowMs(), max)
}

// StoredKeys возвращает число хранимых ключей, включая просроченные, которые ещё не удалены.
// Не имеет побочных эффектов (в отличие от DBSIZE, который лениво удаляет), поэтому годится для тестов сэмплера.
func (e *Engine) StoredKeys() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.db.data)
}

// Execute выполняет одну команду клиента.
func (e *Engine) Execute(s *Session, args [][]byte) resp.Value {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run(s, args)
}

func (e *Engine) nowMs() int64 { return e.now().UnixMilli() }

type ctx struct {
	e    *Engine
	s    *Session
	args [][]byte // args[0] — имя команды
	now  int64
	// prop — аргументы для журнала, если они отличаются от исходных (например, EX превращён в PXAT).
	prop   [][]byte
	noProp bool // команда ничего не изменила, в журнал её не пишем
}

func (c *ctx) key(i int) string { return string(c.args[i]) }

type spec struct {
	arity int // >0 — ровно столько аргументов вместе с именем, <0 — не меньше -arity
	write bool
	fn    func(c *ctx) resp.Value
}

var table map[string]spec

func lookup(name string) (spec, bool) {
	sp, ok := table[strings.ToUpper(name)]
	return sp, ok
}

func arityOK(sp spec, n int) bool {
	if sp.arity >= 0 {
		return n == sp.arity
	}
	return n >= -sp.arity
}

func arityError(name string) resp.Value {
	return resp.Err(fmt.Sprintf("ERR wrong number of arguments for '%s' command", strings.ToLower(name)))
}

func unknownCommand(args [][]byte) resp.Value {
	var sb strings.Builder
	for i, a := range args[1:] {
		if i >= 3 {
			break
		}
		fmt.Fprintf(&sb, "'%s' ", a)
	}
	return resp.Err(fmt.Sprintf("ERR unknown command '%s', with args beginning with: %s", args[0], sb.String()))
}

// run — выполнение команды при уже взятой блокировке.
func (e *Engine) run(s *Session, args [][]byte) resp.Value {
	name := strings.ToUpper(string(args[0]))
	e.commands++

	// Внутри MULTI команды ставятся в очередь, кроме управляющих.
	if s.inMulti {
		switch name {
		case "EXEC", "DISCARD", "MULTI", "QUIT":
		default:
			sp, ok := lookup(name)
			switch {
			case !ok:
				s.dirty = true
				return unknownCommand(args)
			case !arityOK(sp, len(args)):
				s.dirty = true
				return arityError(name)
			}
			s.queue = append(s.queue, args)
			return resp.SimpleString("QUEUED")
		}
	}

	switch name {
	case "MULTI":
		if s.inMulti {
			return resp.Err("ERR MULTI calls can not be nested")
		}
		s.inMulti, s.dirty, s.queue = true, false, nil
		return resp.OK()
	case "DISCARD":
		if !s.inMulti {
			return resp.Err("ERR DISCARD without MULTI")
		}
		s.inMulti, s.dirty, s.queue = false, false, nil
		return resp.OK()
	case "EXEC":
		if !s.inMulti {
			return resp.Err("ERR EXEC without MULTI")
		}
		queue, dirty := s.queue, s.dirty
		s.inMulti, s.dirty, s.queue = false, false, nil
		if dirty {
			return resp.Err("EXECABORT Transaction discarded because of previous errors.")
		}
		out := make([]resp.Value, len(queue))
		for i, q := range queue {
			out[i] = e.dispatch(s, q)
		}
		return resp.ArrayOf(out...)
	}
	return e.dispatch(s, args)
}

func (e *Engine) dispatch(s *Session, args [][]byte) resp.Value {
	name := string(args[0])
	sp, ok := lookup(name)
	if !ok {
		return unknownCommand(args)
	}
	if !arityOK(sp, len(args)) {
		return arityError(name)
	}
	c := &ctx{e: e, s: s, args: args, now: e.nowMs()}
	out := sp.fn(c)
	if sp.write && out.Kind != resp.Error && !c.noProp && e.onWrite != nil && !e.replay {
		if c.prop != nil {
			e.onWrite(c.prop)
		} else {
			e.onWrite(args)
		}
	}
	return out
}

// typed ищет ключ нужного типа: (nil, false) — ключа нет, (nil, true) — тип другой.
func (c *ctx) typed(key string, k kind) (e *entry, wrongType bool) {
	en := c.e.db.lookup(key, c.now)
	if en == nil {
		return nil, false
	}
	if en.kind != k {
		return nil, true
	}
	return en, false
}

func bs(s string) []byte { return []byte(s) }
