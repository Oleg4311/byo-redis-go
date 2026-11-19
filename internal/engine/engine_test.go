package engine

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newTest() (*Engine, *Session, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	return New(WithClock(clk.now)), &Session{}, clk
}

func run(t *testing.T, e *Engine, s *Session, line string) string {
	t.Helper()
	args, err := resp.SplitArgs(line)
	if err != nil {
		t.Fatalf("разбор %q: %v", line, err)
	}
	return resp.FormatCLI(e.Execute(s, args))
}

type step struct{ cmd, want string }

func script(t *testing.T, steps []step) {
	t.Helper()
	e, s, _ := newTest()
	for _, st := range steps {
		if got := run(t, e, s, st.cmd); got != st.want {
			t.Errorf("%s\n  получено:   %q\n  ожидалось: %q", st.cmd, got, st.want)
		}
	}
}

func TestStrings(t *testing.T) {
	script(t, []step{
		{"SET k v", "OK"},
		{"GET k", `"v"`},
		{"GET missing", "(nil)"},
		{"SET k v2 NX", "(nil)"},
		{"GET k", `"v"`},
		{"SET new 1 NX", "OK"},
		{"SET nope 1 XX", "(nil)"},
		{"SET k v3 XX", "OK"},
		{"SET k v4 GET", `"v3"`},
		{"SET none x GET", "(nil)"},
		{"SETNX k z", "(integer) 0"},
		{"SETNX fresh z", "(integer) 1"},
		{"APPEND k -tail", "(integer) 7"},
		{"GET k", `"v4-tail"`},
		{"STRLEN k", "(integer) 7"},
		{"STRLEN missing", "(integer) 0"},
		{"MSET a 1 b 2 c 3", "OK"},
		{"MGET a b zz c", "1) \"1\"\n2) \"2\"\n3) (nil)\n4) \"3\""},
		{"SET k", "(error) ERR wrong number of arguments for 'set' command"},
		{"MSET a 1 b", "(error) ERR wrong number of arguments for 'mset' command"},
	})
}

func TestSetOptionsErrors(t *testing.T) {
	script(t, []step{
		{"SET k v NX XX", "(error) ERR syntax error"},
		{"SET k v EX", "(error) ERR syntax error"},
		{"SET k v EX abc", "(error) ERR value is not an integer or out of range"},
		{"SET k v EX 0", "(error) ERR invalid expire time in 'set' command"},
		{"SET k v EX -5", "(error) ERR invalid expire time in 'set' command"},
		{"SET k v EX 10 PX 100", "(error) ERR syntax error"},
		{"SET k v EX 10 KEEPTTL", "(error) ERR syntax error"},
		{"SET k v BOGUS", "(error) ERR syntax error"},
		{"SET k v EX 9223372036854775807", "(error) ERR invalid expire time in 'set' command"},
		{"GET k", "(nil)"}, // ни одна из неудачных попыток ключ не создала
	})
}

func TestCounters(t *testing.T) {
	script(t, []step{
		{"INCR n", "(integer) 1"},
		{"INCR n", "(integer) 2"},
		{"INCRBY n 10", "(integer) 12"},
		{"DECR n", "(integer) 11"},
		{"DECRBY n 20", "(integer) -9"},
		{"GET n", `"-9"`},
		{"SET s abc", "OK"},
		{"INCR s", "(error) ERR value is not an integer or out of range"},
		{"SET pad 007", "OK"},
		{"INCR pad", "(error) ERR value is not an integer or out of range"},
		{"SET plus +5", "OK"},
		{"INCR plus", "(error) ERR value is not an integer or out of range"},
		{"INCRBY n x", "(error) ERR value is not an integer or out of range"},
		{"SET big 9223372036854775807", "OK"},
		{"INCR big", "(error) ERR increment or decrement would overflow"},
		{"SET small -9223372036854775808", "OK"},
		{"DECR small", "(error) ERR increment or decrement would overflow"},
		{"DECRBY n -9223372036854775808", "(error) ERR decrement would overflow"},
		{"GET big", `"9223372036854775807"`}, // после переполнения значение не изменилось
	})
}

func TestKeys(t *testing.T) {
	script(t, []step{
		{"MSET user:1 a user:2 b order:1 c", "OK"},
		{"DEL user:1 user:1 nope", "(integer) 1"},
		{"EXISTS user:2 user:2 nope", "(integer) 2"},
		{"KEYS user:*", `1) "user:2"`},
		{"KEYS *", "1) \"order:1\"\n2) \"user:2\""},
		{"KEYS o?der:[0-9]", `1) "order:1"`},
		{"TYPE user:2", "string"},
		{"TYPE nope", "none"},
		{"DBSIZE", "(integer) 2"},
		{"FLUSHALL", "OK"},
		{"DBSIZE", "(integer) 0"},
		{"FLUSHDB BOGUS", "(error) ERR syntax error"},
	})
}

func TestWrongType(t *testing.T) {
	const wt = "(error) WRONGTYPE Operation against a key holding the wrong kind of value"
	script(t, []step{
		{"RPUSH l a", "(integer) 1"},
		{"HSET h f v", "(integer) 1"},
		{"SADD s m", "(integer) 1"},
		{"SET str x", "OK"},
		{"GET l", wt}, {"APPEND l x", wt}, {"INCR h", wt}, {"STRLEN s", wt},
		{"LPUSH str x", wt}, {"LLEN h", wt}, {"LRANGE s 0 -1", wt},
		{"HGET l f", wt}, {"HSET str f v", wt}, {"HGETALL s", wt},
		{"SADD l m", wt}, {"SMEMBERS h", wt}, {"SCARD str", wt},
		{"SET l x GET", wt},
		{"TYPE l", "list"}, {"TYPE h", "hash"}, {"TYPE s", "set"},
		{"MGET l str", "1) (nil)\n2) \"x\""}, // MGET не падает на чужих типах
		{"SET l replaced", "OK"},             // SET без GET перезаписывает любой тип
		{"TYPE l", "string"},
	})
}

func TestLists(t *testing.T) {
	script(t, []step{
		{"RPUSH l a b c", "(integer) 3"},
		{"LPUSH l z y", "(integer) 5"},
		{"LRANGE l 0 -1", "1) \"y\"\n2) \"z\"\n3) \"a\"\n4) \"b\"\n5) \"c\""},
		{"LRANGE l 1 2", "1) \"z\"\n2) \"a\""},
		{"LRANGE l -2 -1", "1) \"b\"\n2) \"c\""},
		{"LRANGE l -100 100", "1) \"y\"\n2) \"z\"\n3) \"a\"\n4) \"b\"\n5) \"c\""},
		{"LRANGE l 3 1", "(empty array)"},
		{"LRANGE l 10 20", "(empty array)"},
		{"LRANGE missing 0 -1", "(empty array)"},
		{"LINDEX l 0", `"y"`}, {"LINDEX l -1", `"c"`}, {"LINDEX l 9", "(nil)"}, {"LINDEX l -9", "(nil)"},
		{"LLEN l", "(integer) 5"},
		{"LPOP l", `"y"`},
		{"RPOP l", `"c"`},
		{"LPOP l 2", "1) \"z\"\n2) \"a\""},
		{"RPOP l 10", `1) "b"`},
		{"EXISTS l", "(integer) 0"}, // пустой список удаляется
		{"LPOP l", "(nil)"},
		{"LPOP l 2", "(nil)"},
		{"RPUSH l a", "(integer) 1"},
		{"LPOP l 0", "(empty array)"},
		{"LPOP l -1", "(error) ERR value is out of range, must be positive"},
		{"LPOP l x", "(error) ERR value is not an integer or out of range"},
		{"LRANGE l a b", "(error) ERR value is not an integer or out of range"},
	})
}

func TestHashesAndSets(t *testing.T) {
	script(t, []step{
		{"HSET h a 1 b 2", "(integer) 2"},
		{"HSET h a 10 c 3", "(integer) 1"}, // a обновлено, c добавлено
		{"HGET h a", `"10"`},
		{"HGET h zz", "(nil)"},
		{"HGET nohash a", "(nil)"},
		{"HLEN h", "(integer) 3"},
		{"HEXISTS h b", "(integer) 1"}, {"HEXISTS h q", "(integer) 0"},
		{"HGETALL h", "1) \"a\"\n2) \"10\"\n3) \"b\"\n4) \"2\"\n5) \"c\"\n6) \"3\""},
		{"HKEYS h", "1) \"a\"\n2) \"b\"\n3) \"c\""},
		{"HVALS h", "1) \"10\"\n2) \"2\"\n3) \"3\""},
		{"HDEL h b zz", "(integer) 1"},
		{"HDEL h a c", "(integer) 2"},
		{"EXISTS h", "(integer) 0"},
		{"HGETALL h", "(empty array)"},
		{"HSET h a", "(error) ERR wrong number of arguments for 'hset' command"},

		{"SADD s a b a c", "(integer) 3"},
		{"SMEMBERS s", "1) \"a\"\n2) \"b\"\n3) \"c\""},
		{"SISMEMBER s b", "(integer) 1"}, {"SISMEMBER s q", "(integer) 0"},
		{"SCARD s", "(integer) 3"},
		{"SREM s a q", "(integer) 1"},
		{"SREM s b c", "(integer) 2"},
		{"EXISTS s", "(integer) 0"},
	})
}

func TestExpiryWithClock(t *testing.T) {
	e, s, clk := newTest()
	do := func(cmd string) string { return run(t, e, s, cmd) }
	check := func(cmd, want string) {
		t.Helper()
		if got := do(cmd); got != want {
			t.Errorf("%s: получено %q, ожидалось %q", cmd, got, want)
		}
	}

	check("SET k v EX 10", "OK")
	check("TTL k", "(integer) 10")
	check("PTTL k", "(integer) 10000")
	clk.advance(3500 * time.Millisecond)
	check("PTTL k", "(integer) 6500")
	check("TTL k", "(integer) 7") // округление к ближайшей секунде
	check("GET k", `"v"`)
	clk.advance(6500 * time.Millisecond) // ровно в момент истечения ключа уже нет
	check("GET k", "(nil)")
	check("TTL k", "(integer) -2")
	check("EXISTS k", "(integer) 0")

	check("SET p v PX 1500", "OK")
	clk.advance(1499 * time.Millisecond)
	check("GET p", `"v"`)
	clk.advance(time.Millisecond)
	check("GET p", "(nil)")

	check("SET n v", "OK")
	check("TTL n", "(integer) -1")
	check("EXPIRE n 5", "(integer) 1")
	check("EXPIRE missing 5", "(integer) 0")
	check("PERSIST n", "(integer) 1")
	check("PERSIST n", "(integer) 0")
	check("TTL n", "(integer) -1")

	check("SET kt v EX 100", "OK")
	check("SET kt v2 KEEPTTL", "OK")
	check("TTL kt", "(integer) 100")
	check("SET kt v3", "OK") // обычный SET сбрасывает срок
	check("TTL kt", "(integer) -1")

	check("SET c 1 EX 50", "OK")
	check("INCR c", "(integer) 2") // INCR не трогает срок
	check("TTL c", "(integer) 50")

	check("EXPIRE n 0", "(integer) 1") // срок «сейчас» удаляет ключ
	check("EXISTS n", "(integer) 0")
	check("EXPIRE n abc", "(error) ERR value is not an integer or out of range")

	now := clk.now().Unix()
	check("SET at v EXAT "+itoa(now+30), "OK")
	check("TTL at", "(integer) 30")
	check("SET past v EXAT "+itoa(now-5), "OK") // срок в прошлом: ключ сразу исчезает
	check("EXISTS past", "(integer) 0")

	check("PEXPIREAT at "+itoa((now+60)*1000), "(integer) 1")
	check("TTL at", "(integer) 60")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSweepExpired(t *testing.T) {
	e, s, clk := newTest()
	for i := 0; i < 50; i++ {
		run(t, e, s, "SET k"+itoa(int64(i))+" v PX 100")
	}
	run(t, e, s, "SET keep v")
	if n := e.SweepExpired(1000); n != 0 {
		t.Fatalf("до срока удалено %d", n)
	}
	clk.advance(time.Second)
	if n := e.SweepExpired(20); n != 20 { // лимит сэмпла соблюдается
		t.Fatalf("удалено %d, ожидалось 20", n)
	}
	if n := e.SweepExpired(1000); n != 30 {
		t.Fatalf("удалено %d, ожидалось 30", n)
	}
	if got := run(t, e, s, "DBSIZE"); got != "(integer) 1" {
		t.Fatalf("DBSIZE %s", got)
	}
}

func TestTransactions(t *testing.T) {
	script(t, []step{
		{"MULTI", "OK"},
		{"SET a 1", "QUEUED"},
		{"INCR a", "QUEUED"},
		{"GET a", "QUEUED"},
		{"EXEC", "1) OK\n2) (integer) 2\n3) \"2\""},
		{"EXEC", "(error) ERR EXEC without MULTI"},
		{"DISCARD", "(error) ERR DISCARD without MULTI"},

		{"MULTI", "OK"},
		{"MULTI", "(error) ERR MULTI calls can not be nested"},
		{"SET b 1", "QUEUED"},
		{"DISCARD", "OK"},
		{"GET b", "(nil)"}, // отменённая транзакция ничего не записала

		{"MULTI", "OK"},
		{"SET c 1", "QUEUED"},
		{"NOSUCHCMD", "(error) ERR unknown command 'NOSUCHCMD', with args beginning with: "},
		{"EXEC", "(error) EXECABORT Transaction discarded because of previous errors."},
		{"GET c", "(nil)"},

		{"MULTI", "OK"},
		{"GET", "(error) ERR wrong number of arguments for 'get' command"},
		{"EXEC", "(error) EXECABORT Transaction discarded because of previous errors."},

		// ошибка выполнения внутри EXEC не отменяет остальные команды, как в Redis
		{"SET s text", "OK"},
		{"MULTI", "OK"},
		{"INCR s", "QUEUED"},
		{"SET after 1", "QUEUED"},
		{"EXEC", "1) (error) ERR value is not an integer or out of range\n2) OK"},
		{"GET after", `"1"`},
		{"WATCH a", "(error) ERR WATCH is not supported"},
	})
}

func TestServerCommands(t *testing.T) {
	script(t, []step{
		{"PING", "PONG"},
		{"PING hi", `"hi"`},
		{"PING a b", "(error) ERR wrong number of arguments for 'ping' command"},
		{"ECHO hello", `"hello"`},
		{"SELECT 0", "OK"},
		{"SELECT 1", "(error) ERR DB index is out of range"},
		{"SELECT x", "(error) ERR value is not an integer or out of range"},
		{"command count", "(integer) " + itoa(int64(len(table)))},
		{"COMMAND DOCS", "(empty array)"},
		{"CLIENT SETNAME x", "OK"},
		{"HELLO 3", "(error) NOPROTO this server supports only RESP2"},
		{"HELLO 9", "(error) ERR Protocol version is not an integer or out of range"},
		{"foo bar baz qux quux", "(error) ERR unknown command 'foo', with args beginning with: 'bar' 'baz' 'qux' "},
		{"QUIT", "OK"},
	})
}

func TestInfoAndHello(t *testing.T) {
	e, s, _ := newTest()
	run(t, e, s, "SET a 1")
	run(t, e, s, "SET b 1 EX 100")
	info := run(t, e, s, "INFO")
	for _, want := range []string{"byo_redis_go_version:" + Version, "db0:keys=2,expires=1", "total_commands_processed:"} {
		if !strings.Contains(info, want) {
			t.Errorf("в INFO нет %q: %s", want, info)
		}
	}
	if h := run(t, e, s, "HELLO 2"); !strings.Contains(h, `"proto"`) {
		t.Errorf("HELLO 2: %s", h)
	}
}

func TestQuitSetsClose(t *testing.T) {
	e, s, _ := newTest()
	run(t, e, s, "QUIT")
	if !s.Close {
		t.Fatal("QUIT должен закрывать соединение")
	}
}

func TestOnWritePropagation(t *testing.T) {
	e, s, clk := newTest()
	var log []string
	e.OnWrite(func(args [][]byte) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = string(a)
		}
		log = append(log, strings.Join(parts, " "))
	})
	now := clk.now().UnixMilli()
	run(t, e, s, "SET a 1 EX 10 NX GET")
	run(t, e, s, "SET a 2 NX") // не выполнилась: в журнал не попадает
	run(t, e, s, "GET a")      // чтение: не попадает
	run(t, e, s, "INCR a")
	run(t, e, s, "EXPIRE a 100")
	run(t, e, s, "INCR nonint")
	run(t, e, s, "SET nonint abc")
	run(t, e, s, "INCR nonint") // ошибка: не попадает
	run(t, e, s, "EXPIRE a 0")  // превращается в DEL
	run(t, e, s, "DEL zzz")
	run(t, e, s, "MULTI")
	run(t, e, s, "SET t 1")
	run(t, e, s, "EXEC")
	want := []string{
		"SET a 1 PXAT " + itoa(now+10000),
		"INCR a",
		"PEXPIREAT a " + itoa(now+100000),
		"INCR nonint",
		"SET nonint abc",
		"DEL a",
		"DEL zzz",
		"SET t 1",
	}
	if strings.Join(log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("журнал:\n%s\nожидалось:\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
}

func TestParseInt(t *testing.T) {
	good := map[string]int64{"0": 0, "5": 5, "-5": -5, "9223372036854775807": 9223372036854775807, "-9223372036854775808": -9223372036854775808}
	for s, want := range good {
		if n, ok := parseInt([]byte(s)); !ok || n != want {
			t.Errorf("parseInt(%q) = %d, %v", s, n, ok)
		}
	}
	for _, s := range []string{"", "+1", "01", "-0", "-", "1.5", " 1", "1 ", "abc", "9223372036854775808", "99999999999999999999999"} {
		if _, ok := parseInt([]byte(s)); ok {
			t.Errorf("parseInt(%q) должен отклоняться", s)
		}
	}
}
