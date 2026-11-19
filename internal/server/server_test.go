package server_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Oleg4311/byo-redis-go/internal/aof"
	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/server"
)

func start(t *testing.T, eng *engine.Engine) (addr string, srv *server.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv = server.New(eng, t.Logf)
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close(); <-done })
	return ln.Addr().String(), srv
}

func client(t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr}) // настройки по умолчанию: клиент сам пробует HELLO 3 и должен откатиться на RESP2
	t.Cleanup(func() { c.Close() })
	return c
}

var bg = context.Background()

func TestGoRedisClientBasics(t *testing.T) {
	addr, _ := start(t, engine.New())
	c := client(t, addr)

	if pong, err := c.Ping(bg).Result(); err != nil || pong != "PONG" {
		t.Fatalf("PING: %q %v", pong, err)
	}
	if err := c.Set(bg, "name", "ада", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Get(bg, "name").Result(); err != nil || v != "ада" {
		t.Fatalf("GET: %q %v", v, err)
	}
	if _, err := c.Get(bg, "missing").Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("ожидался redis.Nil, получено %v", err)
	}

	// сроки жизни
	c.Set(bg, "tmp", "x", 30*time.Second)
	if ttl, _ := c.TTL(bg, "tmp").Result(); ttl < 29*time.Second || ttl > 30*time.Second {
		t.Fatalf("TTL: %v", ttl)
	}
	if ok, _ := c.SetNX(bg, "name", "другое", 0).Result(); ok {
		t.Fatal("SetNX не должен перезаписывать")
	}

	// счётчики, списки, хэши, множества
	if n, _ := c.IncrBy(bg, "n", 5).Result(); n != 5 {
		t.Fatalf("INCRBY %d", n)
	}
	c.RPush(bg, "l", "a", "b", "c")
	if got, _ := c.LRange(bg, "l", 0, -1).Result(); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("LRANGE %v", got)
	}
	if v, _ := c.LPop(bg, "l").Result(); v != "a" {
		t.Fatalf("LPOP %q", v)
	}
	c.HSet(bg, "h", map[string]any{"f1": "v1", "f2": "v2"})
	if got, _ := c.HGetAll(bg, "h").Result(); len(got) != 2 || got["f2"] != "v2" {
		t.Fatalf("HGETALL %v", got)
	}
	c.SAdd(bg, "s", "x", "y", "x")
	if n, _ := c.SCard(bg, "s").Result(); n != 2 {
		t.Fatalf("SCARD %d", n)
	}
	if keys, _ := c.Keys(bg, "*").Result(); len(keys) != 6 {
		t.Fatalf("KEYS %v", keys)
	}

	// ошибки приходят как ошибки Redis
	if err := c.LPush(bg, "name", "x").Err(); err == nil || !strings.HasPrefix(err.Error(), "WRONGTYPE") {
		t.Fatalf("ожидался WRONGTYPE, получено %v", err)
	}
	if err := c.Do(bg, "NOSUCH").Err(); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("ожидалась unknown command, получено %v", err)
	}
}

func TestGoRedisPipelineAndTransaction(t *testing.T) {
	addr, _ := start(t, engine.New())
	c := client(t, addr)

	pipe := c.Pipeline()
	for i := 0; i < 100; i++ {
		pipe.Set(bg, fmt.Sprintf("k%d", i), i, 0)
	}
	if _, err := pipe.Exec(bg); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.DBSize(bg).Result(); n != 100 {
		t.Fatalf("DBSIZE %d", n)
	}

	var incr *redis.IntCmd
	var get *redis.StringCmd
	_, err := c.TxPipelined(bg, func(p redis.Pipeliner) error {
		incr = p.Incr(bg, "counter")
		p.Incr(bg, "counter")
		get = p.Get(bg, "counter")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if incr.Val() != 1 || get.Val() != "2" {
		t.Fatalf("транзакция: incr=%d get=%q", incr.Val(), get.Val())
	}
}

// Транзакция выполняется атомарно: наблюдатель никогда не видит a и b в разных состояниях.
func TestTransactionIsAtomic(t *testing.T) {
	addr, _ := start(t, engine.New())
	writer, reader := client(t, addr), client(t, addr)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				writer.TxPipelined(bg, func(p redis.Pipeliner) error {
					p.Incr(bg, "a")
					p.Incr(bg, "b")
					return nil
				})
			}
		}
	}()
	deadline := time.Now().Add(700 * time.Millisecond)
	checks := 0
	for time.Now().Before(deadline) {
		var a, b *redis.StringCmd
		reader.TxPipelined(bg, func(p redis.Pipeliner) error {
			a, b = p.Get(bg, "a"), p.Get(bg, "b")
			return nil
		})
		if a.Val() != b.Val() {
			close(stop)
			t.Fatalf("нарушена атомарность: a=%q b=%q", a.Val(), b.Val())
		}
		checks++
	}
	close(stop)
	wg.Wait()
	if checks < 10 {
		t.Fatalf("слишком мало проверок: %d", checks)
	}
}

func TestConcurrentIncr(t *testing.T) {
	addr, _ := start(t, engine.New())
	const clients, each = 50, 100
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := redis.NewClient(&redis.Options{Addr: addr})
			defer c.Close()
			for j := 0; j < each; j++ {
				if err := c.Incr(bg, "hits").Err(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if v, _ := client(t, addr).Get(bg, "hits").Result(); v != fmt.Sprint(clients*each) {
		t.Fatalf("потеряны обновления: hits=%s", v)
	}
}

func rawConn(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn, bufio.NewReader(conn)
}

func TestRawPipeliningAndInline(t *testing.T) {
	addr, _ := start(t, engine.New())
	conn, rd := rawConn(t, addr)

	// три команды одним пакетом: две в формате RESP, одна inline (как из telnet)
	io.WriteString(conn, "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n*2\r\n$3\r\nGET\r\n$1\r\nk\r\nPING\r\n")
	for _, want := range []string{"+OK", "$1", "v", "+PONG"} {
		line, err := rd.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != want {
			t.Fatalf("получено %q (%v), ожидалось %q", line, err, want)
		}
	}
}

func TestRawFragmentedWrites(t *testing.T) {
	addr, _ := start(t, engine.New())
	conn, rd := rawConn(t, addr)
	msg := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nhello\r\n"
	for i := 0; i < len(msg); i++ { // по одному байту: сервер обязан дождаться полной команды
		io.WriteString(conn, msg[i:i+1])
		time.Sleep(time.Millisecond)
	}
	if line, _ := rd.ReadString('\n'); line != "+OK\r\n" {
		t.Fatalf("получено %q", line)
	}
}

func TestProtocolErrorClosesConnection(t *testing.T) {
	addr, _ := start(t, engine.New())
	conn, rd := rawConn(t, addr)
	io.WriteString(conn, "*1\r\n$abc\r\n")
	line, _ := rd.ReadString('\n')
	if !strings.HasPrefix(line, "-ERR Protocol error") {
		t.Fatalf("получено %q", line)
	}
	if _, err := rd.ReadString('\n'); err == nil {
		t.Fatal("соединение должно быть закрыто после ошибки протокола")
	}
}

func TestQuitClosesConnection(t *testing.T) {
	addr, _ := start(t, engine.New())
	conn, rd := rawConn(t, addr)
	io.WriteString(conn, "QUIT\r\n")
	if line, _ := rd.ReadString('\n'); line != "+OK\r\n" {
		t.Fatalf("получено %q", line)
	}
	if _, err := rd.ReadString('\n'); err != io.EOF {
		t.Fatalf("ожидался EOF, получено %v", err)
	}
}

func TestLargeValue(t *testing.T) {
	addr, _ := start(t, engine.New())
	c := client(t, addr)
	big := strings.Repeat("x\r\n", 1<<19) // ~1,5 МиБ с переводами строк внутри
	if err := c.Set(bg, "big", big, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Get(bg, "big").Result(); v != big {
		t.Fatalf("значение искажено: длина %d вместо %d", len(v), len(big))
	}
}

func TestActiveExpirationWithoutAccess(t *testing.T) {
	eng := engine.New()
	addr, _ := start(t, eng)
	c := client(t, addr)
	for i := 0; i < 200; i++ {
		c.Set(bg, fmt.Sprintf("e%d", i), "v", 50*time.Millisecond)
	}
	c.Set(bg, "keep", "v", 0)
	if n := eng.StoredKeys(); n != 201 {
		t.Fatalf("сразу после записи ожидалось 201 ключ, получено %d", n)
	}
	// К ключам никто не обращается, поэтому ленивое удаление исключено: убрать их может только фоновый сэмплер.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if eng.StoredKeys() == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("фоновое удаление не сработало: осталось %d ключей из 201", eng.StoredKeys())
}

func TestCloseDisconnectsIdleClients(t *testing.T) {
	addr, srv := start(t, engine.New())
	conns := make([]net.Conn, 5)
	for i := range conns {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns[i] = conn
		defer conn.Close()
	}
	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close завис из-за простаивающих клиентов")
	}
}

func TestEndToEndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.aof")

	runOnce := func(body func(c *redis.Client)) {
		eng := engine.New()
		journal, err := aof.Open(path, aof.Always)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := journal.Replay(func(a [][]byte) { eng.Replay(a) }); err != nil {
			t.Fatal(err)
		}
		eng.OnWrite(func(a [][]byte) { journal.Append(a) })
		ln, _ := net.Listen("tcp", "127.0.0.1:0")
		srv := server.New(eng, nil)
		done := make(chan struct{})
		go func() { defer close(done); srv.Serve(ln) }()
		c := redis.NewClient(&redis.Options{Addr: ln.Addr().String()})
		body(c)
		c.Close()
		srv.Close()
		<-done
		journal.Close()
	}

	runOnce(func(c *redis.Client) {
		c.Set(bg, "user", "oleg", 0)
		c.Set(bg, "session", "tok", time.Hour)
		c.RPush(bg, "queue", "a", "b")
		c.HSet(bg, "cfg", "mode", "fast")
		c.Incr(bg, "visits")
		c.Incr(bg, "visits")
		c.Del(bg, "gone")
	})
	runOnce(func(c *redis.Client) {
		if v, _ := c.Get(bg, "user").Result(); v != "oleg" {
			t.Errorf("user=%q", v)
		}
		if ttl, _ := c.TTL(bg, "session").Result(); ttl < 59*time.Minute || ttl > time.Hour {
			t.Errorf("TTL session после рестарта: %v", ttl)
		}
		if l, _ := c.LRange(bg, "queue", 0, -1).Result(); strings.Join(l, "") != "ab" {
			t.Errorf("queue=%v", l)
		}
		if v, _ := c.HGet(bg, "cfg", "mode").Result(); v != "fast" {
			t.Errorf("cfg.mode=%q", v)
		}
		if v, _ := c.Get(bg, "visits").Result(); v != "2" {
			t.Errorf("visits=%q", v)
		}
	})
}

// Совместимость с настоящим redis-cli: тест пропускается, если утилиты нет в PATH (в CI она ставится).
func TestRealRedisCLI(t *testing.T) {
	cli, err := exec.LookPath("redis-cli")
	if err != nil {
		t.Skip("redis-cli не найден в PATH")
	}
	addr, _ := start(t, engine.New())
	host, port, _ := net.SplitHostPort(addr)
	run := func(args ...string) string {
		out, err := exec.Command(cli, append([]string{"-h", host, "-p", port}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("redis-cli %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := run("PING"); got != "PONG" {
		t.Fatalf("PING: %q", got)
	}
	run("SET", "k", "v")
	if got := run("GET", "k"); got != "v" {
		t.Fatalf("GET: %q", got)
	}
	run("RPUSH", "l", "a", "b")
	if got := run("LRANGE", "l", "0", "-1"); got != "a\nb" {
		t.Fatalf("LRANGE: %q", got)
	}
	if got := run("INCR", "n"); got != "1" {
		t.Fatalf("INCR: %q", got)
	}
}
