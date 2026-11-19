package engine

// Хранилище: словарь ключей, типизированные значения и таблица сроков жизни.
// Просроченные ключи удаляются лениво при обращении и «активно» — фоновым сэмплированием (SweepExpired).

type kind uint8

const (
	kString kind = iota + 1
	kList
	kHash
	kSet
)

func (k kind) String() string {
	switch k {
	case kString:
		return "string"
	case kList:
		return "list"
	case kHash:
		return "hash"
	case kSet:
		return "set"
	}
	return "none"
}

type entry struct {
	kind kind
	str  []byte
	list [][]byte // O(n) для вставки в голову: сознательное упрощение, см. README
	om   *omap    // поля хэша или элементы множества
}

// omap — словарь, который помнит порядок вставки. Удаление меняет последний элемент местами с удаляемым,
// поэтому оно O(1), как у хэш-таблицы Redis порядок после удаления не гарантируется.
type omap struct {
	keys []string
	vals [][]byte
	idx  map[string]int
}

func newOmap() *omap { return &omap{idx: map[string]int{}} }

func (m *omap) len() int { return len(m.keys) }

func (m *omap) set(k string, v []byte) (added bool) {
	if i, ok := m.idx[k]; ok {
		m.vals[i] = v
		return false
	}
	m.idx[k] = len(m.keys)
	m.keys = append(m.keys, k)
	m.vals = append(m.vals, v)
	return true
}

func (m *omap) get(k string) ([]byte, bool) {
	i, ok := m.idx[k]
	if !ok {
		return nil, false
	}
	return m.vals[i], true
}

func (m *omap) del(k string) bool {
	i, ok := m.idx[k]
	if !ok {
		return false
	}
	last := len(m.keys) - 1
	if i != last {
		m.keys[i], m.vals[i] = m.keys[last], m.vals[last]
		m.idx[m.keys[i]] = i
	}
	m.keys, m.vals = m.keys[:last], m.vals[:last]
	delete(m.idx, k)
	return true
}

type db struct {
	data map[string]*entry
	exp  map[string]int64 // unix-время истечения в миллисекундах
}

func newDB() *db { return &db{data: map[string]*entry{}, exp: map[string]int64{}} }

// lookup возвращает живой ключ; просроченный удаляется на месте.
func (d *db) lookup(key string, now int64) *entry {
	e := d.data[key]
	if e == nil {
		return nil
	}
	if t, ok := d.exp[key]; ok && t <= now {
		d.remove(key)
		return nil
	}
	return e
}

func (d *db) remove(key string) bool {
	_, ok := d.data[key]
	delete(d.data, key)
	delete(d.exp, key)
	return ok
}

// sweep удаляет просроченные ключи среди не более чем max случайно выбранных. Возвращает число удалённых.
func (d *db) sweep(now int64, max int) int {
	removed, seen := 0, 0
	for key, t := range d.exp { // порядок обхода map в Go случаен, это и есть сэмплирование
		if seen >= max {
			break
		}
		seen++
		if t <= now {
			d.remove(key)
			removed++
		}
	}
	return removed
}
