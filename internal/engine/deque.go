package engine

// deque — двусторонняя очередь из двух стеков: front хранится в обратном порядке, back в прямом.
// Вставка и удаление с любого конца — амортизированное O(1), доступ по индексу — O(1).
// Порядок элементов: front в обратном порядке, затем back.
type deque struct {
	front [][]byte
	back  [][]byte
}

func (d *deque) len() int { return len(d.front) + len(d.back) }

func (d *deque) pushFront(v []byte) { d.front = append(d.front, v) }
func (d *deque) pushBack(v []byte)  { d.back = append(d.back, v) }

// popFront снимает элемент с головы; очередь не должна быть пустой.
func (d *deque) popFront() []byte {
	if n := len(d.front); n > 0 {
		v := d.front[n-1]
		d.front[n-1] = nil // не удерживаем память
		d.front = d.front[:n-1]
		return v
	}
	v := d.back[0]
	d.back[0] = nil
	d.back = d.back[1:]
	return v
}

// popBack снимает элемент с хвоста; очередь не должна быть пустой.
func (d *deque) popBack() []byte {
	if n := len(d.back); n > 0 {
		v := d.back[n-1]
		d.back[n-1] = nil
		d.back = d.back[:n-1]
		return v
	}
	v := d.front[0]
	d.front[0] = nil
	d.front = d.front[1:]
	return v
}

// at возвращает элемент по индексу 0 <= i < len.
func (d *deque) at(i int) []byte {
	if nf := len(d.front); i < nf {
		return d.front[nf-1-i]
	} else {
		return d.back[i-nf]
	}
}

// slice возвращает копию элементов с from по to включительно.
func (d *deque) slice(from, to int) [][]byte {
	out := make([][]byte, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, d.at(i))
	}
	return out
}
