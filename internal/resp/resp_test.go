package resp

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func cmds(t *testing.T, input string) [][]string {
	t.Helper()
	r := NewReader(strings.NewReader(input))
	var out [][]string
	for {
		args, err := r.ReadCommand()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("неожиданная ошибка: %v", err)
		}
		row := make([]string, len(args))
		for i, a := range args {
			row[i] = string(a)
		}
		out = append(out, row)
	}
}

func TestReadArrayCommands(t *testing.T) {
	got := cmds(t, "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$5\r\nhello\r\n*1\r\n$4\r\nPING\r\n")
	if len(got) != 2 || strings.Join(got[0], " ") != "SET k hello" || got[1][0] != "PING" {
		t.Fatalf("получено %v", got)
	}
}

func TestBulkMayContainCRLFAndBinary(t *testing.T) {
	got := cmds(t, "*2\r\n$3\r\nGET\r\n$6\r\na\r\nb\x00c\r\n")
	if got[0][1] != "a\r\nb\x00c" {
		t.Fatalf("получено %q", got[0][1])
	}
}

func TestInlineAndEmptyLines(t *testing.T) {
	got := cmds(t, "\r\nPING\r\n  SET  key  \"hello world\"  \r\n*0\r\nECHO 'a b'\n")
	want := [][]string{{"PING"}, {"SET", "key", "hello world"}, {"ECHO", "a b"}}
	if len(got) != len(want) {
		t.Fatalf("получено %v", got)
	}
	for i := range want {
		if strings.Join(got[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("строка %d: %v, ожидалось %v", i, got[i], want[i])
		}
	}
}

func TestSplitArgsEscapes(t *testing.T) {
	args, err := SplitArgs(`SET k "a\x41\n\"q\""`)
	if err != nil {
		t.Fatal(err)
	}
	if string(args[2]) != "aA\n\"q\"" {
		t.Fatalf("получено %q", args[2])
	}
	for _, bad := range []string{`"unclosed`, `'unclosed`, `"a"b`, `'a'b`} {
		if _, err := SplitArgs(bad); err == nil {
			t.Errorf("ожидалась ошибка для %q", bad)
		}
	}
}

func TestProtocolErrors(t *testing.T) {
	cases := map[string]string{
		"*x\r\n":               "invalid multibulk length",
		"*1\r\nfoo\r\n":        "expected '$'",
		"*1\r\n$x\r\n":         "invalid bulk length",
		"*1\r\n$-5\r\n":        "invalid bulk length",
		"*1\r\n$100000000\r\n": "invalid bulk length",
		"*1\r\n$3\r\nabcXY":    "not terminated by CRLF",
		"*2000000\r\n":         "invalid multibulk length",
	}
	for in, want := range cases {
		_, err := NewReader(strings.NewReader(in)).ReadCommand()
		var pe *ProtocolError
		if !errors.As(err, &pe) || !strings.Contains(pe.Msg, want) {
			t.Errorf("%q: ошибка %v, ожидалось %q", in, err, want)
		}
	}
}

func TestTruncatedInputIsUnexpectedEOF(t *testing.T) {
	for _, in := range []string{"*2\r\n$3\r\nGET\r\n", "*1\r\n$5\r\nab", "PING"} {
		_, err := NewReader(strings.NewReader(in)).ReadCommand()
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("%q: ошибка %v", in, err)
		}
	}
	if _, err := NewReader(strings.NewReader("")).ReadCommand(); err != io.EOF {
		t.Errorf("пустой ввод: %v", err)
	}
}

func TestMarshal(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{OK(), "+OK\r\n"},
		{Err("ERR boom"), "-ERR boom\r\n"},
		{Int(-42), ":-42\r\n"},
		{BulkString("hi"), "$2\r\nhi\r\n"},
		{BulkString(""), "$0\r\n\r\n"},
		{NullBulk(), "$-1\r\n"},
		{NullArray(), "*-1\r\n"},
		{ArrayOf(), "*0\r\n"},
		{ArrayOf(Int(1), ArrayOf(BulkString("a"), NullBulk())), "*2\r\n:1\r\n*2\r\n$1\r\na\r\n$-1\r\n"},
		{Err("bad\r\nline"), "-bad  line\r\n"}, // переводы строк в ошибке не ломают протокол
	}
	for _, c := range cases {
		if got := string(Marshal(c.v)); got != c.want {
			t.Errorf("получено %q, ожидалось %q", got, c.want)
		}
	}
}

func TestRoundTripCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, BulkArray([][]byte{[]byte("SET"), []byte("k"), []byte("v\r\nx")})); err != nil {
		t.Fatal(err)
	}
	args, err := NewReader(&buf).ReadCommand()
	if err != nil || string(args[2]) != "v\r\nx" {
		t.Fatalf("%q %v", args, err)
	}
}

func TestFormatCLI(t *testing.T) {
	cases := []struct {
		v    Value
		want string
	}{
		{OK(), "OK"},
		{Int(3), "(integer) 3"},
		{NullBulk(), "(nil)"},
		{Err("ERR x"), "(error) ERR x"},
		{BulkString("a\"b\n\x01é"), `"a\"b\n\x01\xc3\xa9"`},
		{ArrayOf(), "(empty array)"},
		{ArrayOf(BulkString("a"), BulkString("b")), "1) \"a\"\n2) \"b\""},
		{ArrayOf(Int(1), ArrayOf(BulkString("x"), BulkString("y"))), "1) (integer) 1\n2) 1) \"x\"\n   2) \"y\""},
	}
	for _, c := range cases {
		if got := FormatCLI(c.v); got != c.want {
			t.Errorf("получено %q, ожидалось %q", got, c.want)
		}
	}
}
