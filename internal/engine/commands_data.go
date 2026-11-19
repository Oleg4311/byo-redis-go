package engine

import (
	"github.com/Oleg4311/byo-redis-go/internal/resp"
)

// ---- списки ----

func push(c *ctx, front bool) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kList)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		en = &entry{kind: kList}
		c.e.db.data[key] = en
	}
	for _, v := range c.args[2:] {
		if front {
			en.list.pushFront(v)
		} else {
			en.list.pushBack(v)
		}
	}
	return resp.Int(int64(en.list.len()))
}

func cmdLPush(c *ctx) resp.Value { return push(c, true) }
func cmdRPush(c *ctx) resp.Value { return push(c, false) }

func pop(c *ctx, front bool) resp.Value {
	key := c.key(1)
	hasCount := len(c.args) == 3
	var count int64 = 1
	if hasCount {
		n, ok := parseInt(c.args[2])
		if !ok {
			return resp.Err(errNotInt)
		}
		if n < 0 {
			return resp.Err("ERR value is out of range, must be positive")
		}
		count = n
	}
	en, wrong := c.typed(key, kList)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		c.noProp = true
		if hasCount {
			return resp.NullArray()
		}
		return resp.NullBulk()
	}
	if count > int64(en.list.len()) {
		count = int64(en.list.len())
	}
	taken := make([][]byte, 0, count)
	for i := int64(0); i < count; i++ {
		if front {
			taken = append(taken, en.list.popFront())
		} else {
			taken = append(taken, en.list.popBack())
		}
	}
	if en.list.len() == 0 {
		c.e.db.remove(key)
	}
	if !hasCount && len(taken) == 0 { // недостижимо: пустые списки не хранятся, но так нет паники при нарушении инварианта
		return resp.NullBulk()
	}
	if hasCount {
		return resp.BulkArray(taken)
	}
	return resp.BulkBytes(taken[0])
}

func cmdLPop(c *ctx) resp.Value { return pop(c, true) }
func cmdRPop(c *ctx) resp.Value { return pop(c, false) }

func cmdLLen(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kList)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	return resp.Int(int64(en.list.len()))
}

func cmdLRange(c *ctx) resp.Value {
	start, ok1 := parseInt(c.args[2])
	stop, ok2 := parseInt(c.args[3])
	if !ok1 || !ok2 {
		return resp.Err(errNotInt)
	}
	en, wrong := c.typed(c.key(1), kList)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.ArrayOf()
	}
	n := int64(en.list.len())
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if stop < 0 {
		stop += n
	}
	if start > stop || start >= n {
		return resp.ArrayOf()
	}
	if stop >= n {
		stop = n - 1
	}
	return resp.BulkArray(en.list.slice(int(start), int(stop)))
}

func cmdLIndex(c *ctx) resp.Value {
	idx, ok := parseInt(c.args[2])
	if !ok {
		return resp.Err(errNotInt)
	}
	en, wrong := c.typed(c.key(1), kList)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.NullBulk()
	}
	n := int64(en.list.len())
	if idx < 0 {
		idx += n
	}
	if idx < 0 || idx >= n {
		return resp.NullBulk()
	}
	return resp.BulkBytes(en.list.at(int(idx)))
}

// ---- хэши ----

func cmdHSet(c *ctx) resp.Value {
	if len(c.args)%2 != 0 {
		return arityError("hset")
	}
	key := c.key(1)
	en, wrong := c.typed(key, kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		en = &entry{kind: kHash, om: newOmap()}
		c.e.db.data[key] = en
	}
	var added int64
	for i := 2; i < len(c.args); i += 2 {
		if en.om.set(string(c.args[i]), c.args[i+1]) {
			added++
		}
	}
	return resp.Int(added)
}

func cmdHGet(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.NullBulk()
	}
	v, ok := en.om.get(c.key(2))
	if !ok {
		return resp.NullBulk()
	}
	return resp.BulkBytes(v)
}

func cmdHDel(c *ctx) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		c.noProp = true
		return resp.Int(0)
	}
	var n int64
	for _, f := range c.args[2:] {
		if en.om.del(string(f)) {
			n++
		}
	}
	if en.om.len() == 0 {
		c.e.db.remove(key)
	}
	return resp.Int(n)
}

func hashList(c *ctx, keys, vals bool) resp.Value {
	en, wrong := c.typed(c.key(1), kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.ArrayOf()
	}
	out := make([]resp.Value, 0, en.om.len()*2)
	for i, k := range en.om.keys {
		if keys {
			out = append(out, resp.BulkString(k))
		}
		if vals {
			out = append(out, resp.BulkBytes(en.om.vals[i]))
		}
	}
	return resp.ArrayOf(out...)
}

func cmdHGetAll(c *ctx) resp.Value { return hashList(c, true, true) }
func cmdHKeys(c *ctx) resp.Value   { return hashList(c, true, false) }
func cmdHVals(c *ctx) resp.Value   { return hashList(c, false, true) }

func cmdHLen(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	return resp.Int(int64(en.om.len()))
}

func cmdHExists(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kHash)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	if _, ok := en.om.get(c.key(2)); ok {
		return resp.Int(1)
	}
	return resp.Int(0)
}

// ---- множества ----

func cmdSAdd(c *ctx) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kSet)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		en = &entry{kind: kSet, om: newOmap()}
		c.e.db.data[key] = en
	}
	var added int64
	for _, m := range c.args[2:] {
		if en.om.set(string(m), nil) {
			added++
		}
	}
	return resp.Int(added)
}

func cmdSRem(c *ctx) resp.Value {
	key := c.key(1)
	en, wrong := c.typed(key, kSet)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		c.noProp = true
		return resp.Int(0)
	}
	var n int64
	for _, m := range c.args[2:] {
		if en.om.del(string(m)) {
			n++
		}
	}
	if en.om.len() == 0 {
		c.e.db.remove(key)
	}
	return resp.Int(n)
}

func cmdSMembers(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kSet)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.ArrayOf()
	}
	out := make([]resp.Value, len(en.om.keys))
	for i, k := range en.om.keys {
		out[i] = resp.BulkString(k)
	}
	return resp.ArrayOf(out...)
}

func cmdSIsMember(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kSet)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	if _, ok := en.om.get(c.key(2)); ok {
		return resp.Int(1)
	}
	return resp.Int(0)
}

func cmdSCard(c *ctx) resp.Value {
	en, wrong := c.typed(c.key(1), kSet)
	if wrong {
		return resp.Err(errWrongType)
	}
	if en == nil {
		return resp.Int(0)
	}
	return resp.Int(int64(en.om.len()))
}
