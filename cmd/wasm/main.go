//go:build js && wasm

// Command wasm запускает движок прямо в браузере: byoRedisExec(строка) возвращает ответ в формате redis-cli.
// Сети нет, это тот же код команд, что и в TCP-сервере.
package main

import (
	"syscall/js"

	"github.com/Oleg4311/byo-redis-go/internal/engine"
	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

var (
	eng  = engine.New()
	sess = &engine.Session{}
)

func execLine(_ js.Value, args []js.Value) any {
	parts, err := resp.SplitArgs(args[0].String())
	if err != nil {
		return "(error) ERR " + err.Error()
	}
	if len(parts) == 0 {
		return ""
	}
	out := resp.FormatCLI(eng.Execute(sess, parts))
	sess.Close = false // QUIT в браузере ничего не закрывает
	return out
}

func reset(_ js.Value, _ []js.Value) any {
	eng = engine.New()
	sess = &engine.Session{}
	return nil
}

func main() {
	js.Global().Set("byoRedisExec", js.FuncOf(execLine))
	js.Global().Set("byoRedisReset", js.FuncOf(reset))
	select {}
}
