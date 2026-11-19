package engine_test

import (
	"fmt"

	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// Пример сессии: вывод проверяется при каждом запуске тестов, поэтому README не может разойтись с кодом.
func ExampleEngine_Execute() {
	e := engine.New()
	s := &engine.Session{}
	for _, line := range []string{
		`SET greeting "hello world" EX 60`,
		`GET greeting`,
		`INCRBY visits 5`,
		`RPUSH queue job1 job2 job3`,
		`LPOP queue 2`,
		`HSET user:1 name Ada role admin`,
		`HGETALL user:1`,
		`MULTI`,
		`DECRBY visits 2`,
		`GET visits`,
		`EXEC`,
		`LPUSH greeting x`,
	} {
		args, _ := resp.SplitArgs(line)
		fmt.Printf("> %s\n%s\n", line, resp.FormatCLI(e.Execute(s, args)))
	}
	// Output:
	// > SET greeting "hello world" EX 60
	// OK
	// > GET greeting
	// "hello world"
	// > INCRBY visits 5
	// (integer) 5
	// > RPUSH queue job1 job2 job3
	// (integer) 3
	// > LPOP queue 2
	// 1) "job1"
	// 2) "job2"
	// > HSET user:1 name Ada role admin
	// (integer) 2
	// > HGETALL user:1
	// 1) "name"
	// 2) "Ada"
	// 3) "role"
	// 4) "admin"
	// > MULTI
	// OK
	// > DECRBY visits 2
	// QUEUED
	// > GET visits
	// QUEUED
	// > EXEC
	// 1) (integer) 3
	// 2) "3"
	// > LPUSH greeting x
	// (error) WRONGTYPE Operation against a key holding the wrong kind of value
}
