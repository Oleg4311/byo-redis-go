package engine

import (
	"bytes"
	"math/rand"
	"strconv"
	"testing"
)

// Случайные операции сверяются с обычным слайсом, который заведомо работает правильно.
func TestDequeAgainstSliceModel(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var d deque
		var model [][]byte
		for step := 0; step < 2000; step++ {
			switch op := rng.Intn(5); {
			case op == 0 || (op >= 3 && len(model) == 0):
				v := []byte(strconv.Itoa(step))
				d.pushFront(v)
				model = append([][]byte{v}, model...)
			case op == 1:
				v := []byte(strconv.Itoa(step))
				d.pushBack(v)
				model = append(model, v)
			case op == 2 && len(model) > 0:
				if got, want := d.popFront(), model[0]; !bytes.Equal(got, want) {
					t.Fatalf("seed %d шаг %d popFront: %q != %q", seed, step, got, want)
				}
				model = model[1:]
			case op == 3 && len(model) > 0:
				if got, want := d.popBack(), model[len(model)-1]; !bytes.Equal(got, want) {
					t.Fatalf("seed %d шаг %d popBack: %q != %q", seed, step, got, want)
				}
				model = model[:len(model)-1]
			}
			if d.len() != len(model) {
				t.Fatalf("seed %d шаг %d: длина %d != %d", seed, step, d.len(), len(model))
			}
			if len(model) > 0 {
				i := rng.Intn(len(model))
				if !bytes.Equal(d.at(i), model[i]) {
					t.Fatalf("seed %d шаг %d at(%d): %q != %q", seed, step, i, d.at(i), model[i])
				}
				j := i + rng.Intn(len(model)-i)
				got := d.slice(i, j)
				for k := range got {
					if !bytes.Equal(got[k], model[i+k]) {
						t.Fatalf("seed %d шаг %d slice(%d,%d)[%d]", seed, step, i, j, k)
					}
				}
			}
		}
	}
}

func BenchmarkLPush(b *testing.B) {
	var d deque
	v := []byte("x")
	for i := 0; i < b.N; i++ {
		d.pushFront(v)
	}
}
