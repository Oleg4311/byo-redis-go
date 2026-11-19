package aof

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

func cmd(parts ...string) [][]byte {
	out := make([][]byte, len(parts))
	for i, p := range parts {
		out[i] = []byte(p)
	}
	return out
}

func replayInto(t *testing.T, path string, e *engine.Engine) (n int, truncated bool, err error) {
	t.Helper()
	l, err := Open(path, No)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Replay(func(args [][]byte) { e.Replay(args) })
}

func get(e *engine.Engine, key string) string {
	return resp.FormatCLI(e.Execute(&engine.Session{}, cmd("GET", key)))
}

func TestAppendReplayRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.aof")
	l, err := Open(path, Always)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][][]byte{
		cmd("SET", "a", "1"),
		cmd("SET", "bin", "x\r\ny\x00z"),
		cmd("RPUSH", "l", "p", "q"),
		cmd("DEL", "a"),
	} {
		if err := l.Append(c); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	e := engine.New()
	n, truncated, err := replayInto(t, path, e)
	if err != nil || truncated || n != 4 {
		t.Fatalf("n=%d truncated=%v err=%v", n, truncated, err)
	}
	if got := get(e, "a"); got != "(nil)" {
		t.Errorf("a: %s", got)
	}
	if got := get(e, "bin"); got != `"x\r\ny\x00z"` {
		t.Errorf("bin: %s", got)
	}
	if got := resp.FormatCLI(e.Execute(&engine.Session{}, cmd("LRANGE", "l", "0", "-1"))); got != "1) \"p\"\n2) \"q\"" {
		t.Errorf("l: %s", got)
	}
}

func TestAppendAfterReplayContinuesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.aof")
	l, _ := Open(path, No)
	l.Append(cmd("SET", "a", "1"))
	l.Close()

	l2, _ := Open(path, No)
	if _, _, err := l2.Replay(func([][]byte) {}); err != nil {
		t.Fatal(err)
	}
	l2.Append(cmd("SET", "b", "2"))
	l2.Close()

	e := engine.New()
	n, _, err := replayInto(t, path, e)
	if err != nil || n != 2 || get(e, "a") != `"1"` || get(e, "b") != `"2"` {
		t.Fatalf("n=%d err=%v a=%s b=%s", n, err, get(e, "a"), get(e, "b"))
	}
}

func TestTornTailIsTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.aof")
	l, _ := Open(path, No)
	l.Append(cmd("SET", "a", "1"))
	l.Append(cmd("SET", "b", "2"))
	l.Close()
	good, _ := os.ReadFile(path)
	// питание отключилось посреди записи третьей команды
	torn := append(append([]byte{}, good...), []byte("*3\r\n$3\r\nSET\r\n$1\r\nc\r\n$10\r\n12345")...)
	os.WriteFile(path, torn, 0o644)

	e := engine.New()
	n, truncated, err := replayInto(t, path, e)
	if err != nil || !truncated || n != 2 {
		t.Fatalf("n=%d truncated=%v err=%v", n, truncated, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(good) {
		t.Fatalf("файл не усечён до последней целой команды: %q", after)
	}
	if get(e, "c") != "(nil)" || get(e, "b") != `"2"` {
		t.Fatal("состояние после усечения неверно")
	}
	// после усечения журнал снова читается без потерь
	n, truncated, err = replayInto(t, path, engine.New())
	if err != nil || truncated || n != 2 {
		t.Fatalf("повторное чтение: n=%d truncated=%v err=%v", n, truncated, err)
	}
}

func TestCorruptionInMiddleIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.aof")
	data := "*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n*3\r\nGARBAGE\r\n*3\r\n$3\r\nSET\r\n$1\r\nb\r\n$1\r\n2\r\n"
	os.WriteFile(path, []byte(data), 0o644)
	_, truncated, err := replayInto(t, path, engine.New())
	if !errors.Is(err, ErrCorrupt) || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	if after, _ := os.ReadFile(path); string(after) != data {
		t.Fatal("повреждённый журнал не должен изменяться")
	}
}

func TestExpiryIsAbsoluteAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.aof")
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	clk := &now
	e := engine.New(engine.WithClock(func() time.Time { return *clk }))
	l, _ := Open(path, No)
	e.OnWrite(func(args [][]byte) { l.Append(args) })
	s := &engine.Session{}
	e.Execute(s, cmd("SET", "session", "tok", "EX", "60"))
	e.Execute(s, cmd("SET", "forever", "x"))
	e.Execute(s, cmd("SET", "other", "y"))
	e.Execute(s, cmd("EXPIRE", "other", "10"))
	l.Close()

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "PXAT") || !strings.Contains(string(data), "PEXPIREAT") {
		t.Fatalf("сроки должны быть абсолютными:\n%q", data)
	}

	// «перезапуск» через 30 секунд: session ещё жив, other (10 с) уже нет
	later := now.Add(30 * time.Second)
	e2 := engine.New(engine.WithClock(func() time.Time { return later }))
	if _, _, err := replayInto(t, path, e2); err != nil {
		t.Fatal(err)
	}
	if get(e2, "session") != `"tok"` || get(e2, "forever") != `"x"` || get(e2, "other") != "(nil)" {
		t.Fatalf("session=%s forever=%s other=%s", get(e2, "session"), get(e2, "forever"), get(e2, "other"))
	}
	if ttl := resp.FormatCLI(e2.Execute(&engine.Session{}, cmd("TTL", "session"))); ttl != "(integer) 30" {
		t.Fatalf("TTL после рестарта: %s", ttl)
	}
}

func TestParsePolicy(t *testing.T) {
	for _, ok := range []string{"always", "everysec", "no"} {
		if _, err := ParsePolicy(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if _, err := ParsePolicy("sometimes"); err == nil {
		t.Error("ожидалась ошибка")
	}
}

func TestEverySecSyncsAndCloses(t *testing.T) {
	l, err := Open(filepath.Join(t.TempDir(), "x.aof"), EverySec)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(cmd("SET", "a", "1"))
	time.Sleep(1200 * time.Millisecond) // фоновая синхронизация успела сработать
	l.mu.Lock()
	dirty := l.dirty
	l.mu.Unlock()
	if dirty {
		t.Error("everysec должен сбросить данные на диск")
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
