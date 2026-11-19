package engine

import (
	"fmt"
	"strings"

	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

func init() {
	table = map[string]spec{
		// строки
		"GET": {2, false, cmdGet}, "SET": {-3, true, cmdSet}, "SETNX": {3, true, cmdSetNX},
		"MSET": {-3, true, cmdMSet}, "MGET": {-2, false, cmdMGet},
		"APPEND": {3, true, cmdAppend}, "STRLEN": {2, false, cmdStrlen},
		"INCR": {2, true, cmdIncr}, "DECR": {2, true, cmdDecr},
		"INCRBY": {3, true, cmdIncrBy}, "DECRBY": {3, true, cmdDecrBy},
		// ключи
		"DEL": {-2, true, cmdDel}, "EXISTS": {-2, false, cmdExists},
		"EXPIRE": {3, true, expireCmd(1000, true)}, "PEXPIRE": {3, true, expireCmd(1, true)},
		"EXPIREAT": {3, true, expireCmd(1000, false)}, "PEXPIREAT": {3, true, expireCmd(1, false)},
		"TTL": {2, false, ttlCmd(1000)}, "PTTL": {2, false, ttlCmd(1)},
		"PERSIST": {2, true, cmdPersist}, "TYPE": {2, false, cmdType},
		"KEYS": {2, false, cmdKeys}, "DBSIZE": {1, false, cmdDBSize},
		"FLUSHALL": {-1, true, cmdFlush}, "FLUSHDB": {-1, true, cmdFlush},
		// списки
		"LPUSH": {-3, true, cmdLPush}, "RPUSH": {-3, true, cmdRPush},
		"LPOP": {-2, true, cmdLPop}, "RPOP": {-2, true, cmdRPop},
		"LLEN": {2, false, cmdLLen}, "LRANGE": {4, false, cmdLRange}, "LINDEX": {3, false, cmdLIndex},
		// хэши
		"HSET": {-4, true, cmdHSet}, "HGET": {3, false, cmdHGet}, "HDEL": {-3, true, cmdHDel},
		"HGETALL": {2, false, cmdHGetAll}, "HKEYS": {2, false, cmdHKeys}, "HVALS": {2, false, cmdHVals},
		"HLEN": {2, false, cmdHLen}, "HEXISTS": {3, false, cmdHExists},
		// множества
		"SADD": {-3, true, cmdSAdd}, "SREM": {-3, true, cmdSRem}, "SMEMBERS": {2, false, cmdSMembers},
		"SISMEMBER": {3, false, cmdSIsMember}, "SCARD": {2, false, cmdSCard},
		// служебные
		"PING": {-1, false, cmdPing}, "ECHO": {2, false, cmdEcho}, "SELECT": {2, false, cmdSelect},
		"INFO": {-1, false, cmdInfo}, "COMMAND": {-1, false, cmdCommand}, "CONFIG": {-2, false, cmdConfig},
		"CLIENT": {-2, false, cmdClient}, "HELLO": {-1, false, cmdHello}, "QUIT": {-1, false, cmdQuit},
		"WATCH": {-2, false, cmdWatch}, "UNWATCH": {1, false, func(*ctx) resp.Value { return resp.OK() }},
	}
}

func cmdPing(c *ctx) resp.Value {
	switch len(c.args) {
	case 1:
		return resp.SimpleString("PONG")
	case 2:
		return resp.BulkBytes(c.args[1])
	}
	return arityError("ping")
}

func cmdEcho(c *ctx) resp.Value { return resp.BulkBytes(c.args[1]) }

func cmdSelect(c *ctx) resp.Value {
	n, ok := parseInt(c.args[1])
	if !ok {
		return resp.Err(errNotInt)
	}
	if n != 0 {
		return resp.Err("ERR DB index is out of range") // поддерживается одна база
	}
	return resp.OK()
}

func cmdInfo(c *ctx) resp.Value {
	var keys, withTTL int
	for k := range c.e.db.data {
		if c.e.db.lookup(k, c.now) != nil {
			keys++
			if _, ok := c.e.db.exp[k]; ok {
				withTTL++
			}
		}
	}
	uptime := (c.now - c.e.started.UnixMilli()) / 1000
	s := fmt.Sprintf("# Server\r\nbyo_redis_go_version:%s\r\nredis_mode:standalone\r\nuptime_in_seconds:%d\r\n\r\n"+
		"# Stats\r\ntotal_commands_processed:%d\r\n\r\n# Keyspace\r\ndb0:keys=%d,expires=%d\r\n",
		Version, uptime, c.e.commands, keys, withTTL)
	return resp.BulkString(s)
}

func cmdCommand(c *ctx) resp.Value {
	if len(c.args) >= 2 && strings.EqualFold(string(c.args[1]), "COUNT") {
		return resp.Int(int64(len(table)))
	}
	return resp.ArrayOf() // клиенты вроде redis-cli спрашивают COMMAND DOCS для подсказок, пустой ответ допустим
}

func cmdConfig(c *ctx) resp.Value {
	if strings.EqualFold(string(c.args[1]), "GET") {
		return resp.ArrayOf()
	}
	return resp.Err("ERR unsupported CONFIG subcommand")
}

func cmdClient(c *ctx) resp.Value {
	switch strings.ToUpper(string(c.args[1])) {
	case "SETNAME", "SETINFO":
		return resp.OK()
	case "ID":
		return resp.Int(1)
	}
	return resp.Err("ERR unsupported CLIENT subcommand")
}

// cmdHello отвечает на HELLO: поддерживается только RESP2, поэтому HELLO 3 отклоняется, и клиенты остаются на RESP2.
func cmdHello(c *ctx) resp.Value {
	if len(c.args) >= 2 {
		if n, ok := parseInt(c.args[1]); !ok || (n != 2 && n != 3) {
			return resp.Err("ERR Protocol version is not an integer or out of range")
		} else if n == 3 {
			return resp.Err("NOPROTO this server supports only RESP2")
		}
	}
	return resp.ArrayOf(
		resp.BulkString("server"), resp.BulkString("byo-redis-go"),
		resp.BulkString("version"), resp.BulkString(Version),
		resp.BulkString("proto"), resp.Int(2),
		resp.BulkString("id"), resp.Int(1),
		resp.BulkString("mode"), resp.BulkString("standalone"),
		resp.BulkString("role"), resp.BulkString("master"),
		resp.BulkString("modules"), resp.ArrayOf(),
	)
}

func cmdQuit(c *ctx) resp.Value {
	c.s.Close = true
	return resp.OK()
}

func cmdWatch(c *ctx) resp.Value { return resp.Err("ERR WATCH is not supported") }
