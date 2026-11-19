package engine

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// parseInt разбирает целое так же строго, как Redis: без плюса, пробелов и ведущих нулей.
func parseInt(b []byte) (int64, bool) {
	s := string(b)
	if s == "" || len(s) > 20 {
		return 0, false
	}
	digits := s
	if s[0] == '-' {
		digits = s[1:]
	}
	if digits == "" || (digits[0] == '0' && len(digits) > 1) || (s[0] == '-' && digits == "0") {
		return 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func cmdGet(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kString)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.NullBulk()
	}
	return resp.BulkBytes(en.str)
}

// cmdSet: SET key value [NX|XX] [GET] [EX s|PX ms|EXAT s|PXAT ms|KEEPTTL].
func cmdSet(c *ctx) resp.Value {
	key, val := c.key(1), c.args[2]
	var nx, xx, get, keepTTL, haveExp bool
	var abs int64
	for i := 3; i < len(c.args); i++ {
		switch strings.ToUpper(string(c.args[i])) {
		case "NX":
			if xx {
				return resp.Err(errSyntax)
			}
			nx = true
		case "XX":
			if nx {
				return resp.Err(errSyntax)
			}
			xx = true
		case "GET":
			get = true
		case "KEEPTTL":
			if haveExp {
				return resp.Err(errSyntax)
			}
			keepTTL = true
		case "EX", "PX", "EXAT", "PXAT":
			opt := strings.ToUpper(string(c.args[i]))
			if haveExp || keepTTL || i+1 >= len(c.args) {
				return resp.Err(errSyntax)
			}
			n, ok := parseInt(c.args[i+1])
			if !ok {
				return resp.Err(errNotInt)
			}
			i++
			bad := resp.Err("ERR invalid expire time in 'set' command")
			if n <= 0 {
				return bad
			}
			switch opt {
			case "EX", "EXAT":
				if n > math.MaxInt64/1000 {
					return bad
				}
				abs = n * 1000
			default:
				abs = n
			}
			if opt == "EX" || opt == "PX" {
				if abs > math.MaxInt64-c.now {
					return bad
				}
				abs += c.now
			}
			haveExp = true
		default:
			return resp.Err(errSyntax)
		}
	}

	old := c.e.db.lookup(key, c.now)
	if get && old != nil && old.kind != kString {
		return resp.Err(errWrongType)
	}
	var prev resp.Value = resp.NullBulk()
	if get && old != nil {
		prev = resp.BulkBytes(old.str)
	}
	if (nx && old != nil) || (xx && old == nil) {
		c.noProp = true
		return prev
	}

	db := c.e.db
	db.data[key] = &entry{kind: kString, str: val}
	if !keepTTL {
		delete(db.exp, key)
	}
	// В журнал уходит результат, а не условия: NX/XX/GET уже отработали, относительный срок стал абсолютным.
	c.prop = [][]byte{bs("SET"), c.args[1], val}
	switch {
	case haveExp && abs <= c.now:
		db.remove(key)
		c.prop = [][]byte{bs("DEL"), c.args[1]}
	case haveExp:
		db.exp[key] = abs
		c.prop = append(c.prop, bs("PXAT"), bs(strconv.FormatInt(abs, 10)))
	case keepTTL:
		c.prop = append(c.prop, bs("KEEPTTL"))
	}
	if get {
		return prev
	}
	return resp.OK()
}

func cmdSetNX(c *ctx) resp.Value {
	key := c.key(1)
	if c.e.db.lookup(key, c.now) != nil {
		c.noProp = true
		return resp.Int(0)
	}
	c.e.db.data[key] = &entry{kind: kString, str: c.args[2]}
	return resp.Int(1)
}

func cmdMSet(c *ctx) resp.Value {
	if len(c.args)%2 != 1 {
		return arityError("mset")
	}
	for i := 1; i < len(c.args); i += 2 {
		key := c.key(i)
		c.e.db.data[key] = &entry{kind: kString, str: c.args[i+1]}
		delete(c.e.db.exp, key)
	}
	return resp.OK()
}

func cmdMGet(c *ctx) resp.Value {
	out := make([]resp.Value, 0, len(c.args)-1)
	for _, k := range c.args[1:] {
		en := c.e.db.lookup(string(k), c.now)
		if en == nil || en.kind != kString {
			out = append(out, resp.NullBulk())
		} else {
			out = append(out, resp.BulkBytes(en.str))
		}
	}
	return resp.ArrayOf(out...)
}

func cmdAppend(c *ctx) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kString)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		c.e.db.data[key] = &entry{kind: kString, str: c.args[2]}
		return resp.Int(int64(len(c.args[2])))
	}
	en.str = append(en.str[:len(en.str):len(en.str)], c.args[2]...) // принудительная копия: срез мог пересекаться с буфером запроса
	return resp.Int(int64(len(en.str)))
}

func cmdStrlen(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kString)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	return resp.Int(int64(len(en.str)))
}

func incrBy(c *ctx, delta int64) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kString)
	if wrong {
		return resp.Err(errWrongType)
	}
	var cur int64
	if en != nil {
		n, ok := parseInt(en.str)
		if !ok {
			return resp.Err(errNotInt)
		}
		cur = n
	}
	if (delta > 0 && cur > math.MaxInt64-delta) || (delta < 0 && cur < math.MinInt64-delta) {
		return resp.Err(errOverflow)
	}
	cur += delta
	str := strconv.AppendInt(nil, cur, 10)
	if en == nil {
		c.e.db.data[key] = &entry{kind: kString, str: str}
	} else {
		en.str = str // срок жизни сохраняется
	}
	return resp.Int(cur)
}

func cmdIncr(c *ctx) resp.Value { return incrBy(c, 1) }
func cmdDecr(c *ctx) resp.Value { return incrBy(c, -1) }

func cmdIncrBy(c *ctx) resp.Value {
	n, ok := parseInt(c.args[2])
	if !ok {
		return resp.Err(errNotInt)
	}
	return incrBy(c, n)
}

func cmdDecrBy(c *ctx) resp.Value {
	n, ok := parseInt(c.args[2])
	if !ok {
		return resp.Err(errNotInt)
	}
	if n == math.MinInt64 {
		return resp.Err("ERR decrement would overflow")
	}
	return incrBy(c, -n)
}

func cmdDel(c *ctx) resp.Value {
	var n int64
	for _, k := range c.args[1:] {
		if c.e.db.lookup(string(k), c.now) != nil {
			c.e.db.remove(string(k))
			n++
		}
	}
	return resp.Int(n)
}

func cmdExists(c *ctx) resp.Value {
	var n int64
	for _, k := range c.args[1:] {
		if c.e.db.lookup(string(k), c.now) != nil {
			n++ // повторяющиеся ключи считаются повторно, как в Redis
		}
	}
	return resp.Int(n)
}

// setExpire ставит абсолютный срок; срок в прошлом удаляет ключ.
func setExpire(c *ctx, abs int64) resp.Value {
	key := c.key(1)
	if c.e.db.lookup(key, c.now) == nil {
		c.noProp = true
		return resp.Int(0)
	}
	if abs <= c.now {
		c.e.db.remove(key)
		c.prop = [][]byte{bs("DEL"), c.args[1]}
		return resp.Int(1)
	}
	c.e.db.exp[key] = abs
	c.prop = [][]byte{bs("PEXPIREAT"), c.args[1], bs(strconv.FormatInt(abs, 10))}
	return resp.Int(1)
}

func expireCmd(unitMs int64, relative bool) func(c *ctx) resp.Value {
	return func(c *ctx) resp.Value {
		n, ok := parseInt(c.args[2])
		if !ok {
			return resp.Err(errNotInt)
		}
		if n > math.MaxInt64/unitMs || n < math.MinInt64/unitMs {
			return resp.Err("ERR invalid expire time in '" + strings.ToLower(string(c.args[0])) + "' command")
		}
		abs := n * unitMs
		if relative {
			if abs > math.MaxInt64-c.now {
				return resp.Err("ERR invalid expire time in '" + strings.ToLower(string(c.args[0])) + "' command")
			}
			abs += c.now
		}
		return setExpire(c, abs)
	}
}

func ttlCmd(unitMs int64) func(c *ctx) resp.Value {
	return func(c *ctx) resp.Value {
		key := c.key(1)
		if c.e.db.lookup(key, c.now) == nil {
			return resp.Int(-2)
		}
		t, ok := c.e.db.exp[key]
		if !ok {
			return resp.Int(-1)
		}
		return resp.Int((t - c.now + unitMs/2) / unitMs)
	}
}

func cmdPersist(c *ctx) resp.Value {
	key := c.key(1)
	if c.e.db.lookup(key, c.now) != nil {
		if _, ok := c.e.db.exp[key]; ok {
			delete(c.e.db.exp, key)
			return resp.Int(1)
		}
	}
	c.noProp = true
	return resp.Int(0)
}

func cmdType(c *ctx) resp.Value {
	en := c.e.db.lookup(c.key(1), c.now)
	if en == nil {
		return resp.SimpleString("none")
	}
	return resp.SimpleString(en.kind.String())
}

func cmdKeys(c *ctx) resp.Value {
	pattern := c.key(1)
	var keys []string
	for k := range c.e.db.data {
		if c.e.db.lookup(k, c.now) != nil && globMatch(pattern, k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // у Redis порядок произвольный; сортировка делает ответ воспроизводимым
	out := make([]resp.Value, len(keys))
	for i, k := range keys {
		out[i] = resp.BulkString(k)
	}
	return resp.ArrayOf(out...)
}

func cmdDBSize(c *ctx) resp.Value {
	var n int64
	for k := range c.e.db.data {
		if c.e.db.lookup(k, c.now) != nil {
			n++
		}
	}
	return resp.Int(n)
}

func cmdFlush(c *ctx) resp.Value {
	if len(c.args) == 2 {
		if o := strings.ToUpper(string(c.args[1])); o != "ASYNC" && o != "SYNC" {
			return resp.Err(errSyntax)
		}
	}
	c.e.db = newDB()
	return resp.OK()
}
