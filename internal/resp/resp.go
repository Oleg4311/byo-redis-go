// Package resp реализует протокол RESP2: разбор команд клиента и кодирование ответов.
package resp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// Kind — тип значения RESP.
type Kind uint8

const (
	Simple   Kind = iota // +OK
	Error                // -ERR ...
	Integer              // :42
	Bulk                 // $5\r\nhello
	Nil                  // $-1
	Array                // *2 ...
	NilArray             // *-1
)

// Value — значение ответа сервера.
type Value struct {
	Kind Kind
	Str  []byte
	N    int64
	Arr  []Value
}

// Конструкторы ответов.
func OK() Value                   { return Value{Kind: Simple, Str: []byte("OK")} }
func SimpleString(s string) Value { return Value{Kind: Simple, Str: []byte(s)} }
func Err(msg string) Value        { return Value{Kind: Error, Str: []byte(msg)} }
func Int(n int64) Value           { return Value{Kind: Integer, N: n} }
func BulkString(s string) Value   { return Value{Kind: Bulk, Str: []byte(s)} }
func BulkBytes(b []byte) Value    { return Value{Kind: Bulk, Str: b} }
func NullBulk() Value             { return Value{Kind: Nil} }
func NullArray() Value            { return Value{Kind: NilArray} }
func ArrayOf(v ...Value) Value    { return Value{Kind: Array, Arr: v} }

// BulkArray собирает массив bulk-строк.
func BulkArray(items [][]byte) Value {
	arr := make([]Value, len(items))
	for i, it := range items {
		arr[i] = BulkBytes(it)
	}
	return Value{Kind: Array, Arr: arr}
}

// Ограничения защищают сервер от запросов, которые исчерпывают память.
const (
	MaxBulkLen  = 64 << 20 // 64 МиБ
	MaxArrayLen = 1 << 20
	MaxInline   = 64 << 10
)

// ProtocolError — нарушение протокола клиентом; соединение после него закрывается.
type ProtocolError struct{ Msg string }

func (e *ProtocolError) Error() string { return "Protocol error: " + e.Msg }

// Reader читает команды клиента.
type Reader struct{ br *bufio.Reader }

func NewReader(r io.Reader) *Reader { return &Reader{br: bufio.NewReaderSize(r, 16<<10)} }

// Buffered — сколько байт уже прочитано, но не разобрано (нужно для pipelining).
func (r *Reader) Buffered() int { return r.br.Buffered() }

// ReadCommand читает одну команду: массив bulk-строк или inline-строку.
// io.EOF возвращается только на границе команд.
func (r *Reader) ReadCommand() ([][]byte, error) {
	for {
		first, err := r.br.Peek(1)
		if err != nil {
			return nil, err
		}
		var args [][]byte
		if first[0] == '*' {
			args, err = r.readArray()
		} else {
			args, err = r.readInline()
		}
		if err != nil {
			return nil, err
		}
		if len(args) == 0 { // пустая строка или *0: Redis такое молча пропускает
			continue
		}
		return args, nil
	}
}

func (r *Reader) readLine() ([]byte, error) {
	line, err := r.br.ReadBytes('\n')
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	return line, nil
}

func (r *Reader) readInt(line []byte, what string) (int, error) {
	n, err := strconv.Atoi(string(line))
	if err != nil {
		return 0, &ProtocolError{Msg: "invalid " + what + " length"}
	}
	return n, nil
}

func (r *Reader) readArray() ([][]byte, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	n, err := r.readInt(line[1:], "multibulk")
	if err != nil {
		return nil, err
	}
	if n > MaxArrayLen {
		return nil, &ProtocolError{Msg: "invalid multibulk length"}
	}
	if n <= 0 {
		return nil, nil
	}
	args := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		line, err := r.readLine()
		if err != nil {
			return nil, unexpected(err)
		}
		if len(line) == 0 || line[0] != '$' {
			c := byte(' ')
			if len(line) > 0 {
				c = line[0]
			}
			return nil, &ProtocolError{Msg: fmt.Sprintf("expected '$', got '%c'", c)}
		}
		size, err := r.readInt(line[1:], "bulk")
		if err != nil {
			return nil, err
		}
		if size < 0 || size > MaxBulkLen {
			return nil, &ProtocolError{Msg: "invalid bulk length"}
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r.br, buf); err != nil {
			return nil, unexpected(err)
		}
		if buf[size] != '\r' || buf[size+1] != '\n' {
			return nil, &ProtocolError{Msg: "bulk string is not terminated by CRLF"}
		}
		args = append(args, buf[:size])
	}
	return args, nil
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (r *Reader) readInline() ([][]byte, error) {
	line, err := r.readLine()
	if err != nil {
		return nil, err
	}
	if len(line) > MaxInline {
		return nil, &ProtocolError{Msg: "too big inline request"}
	}
	return SplitArgs(string(line))
}

// SplitArgs разбирает inline-команду: пробелы разделяют аргументы, "..." и '...' группируют, \x41 и \n экранируют.
func SplitArgs(line string) ([][]byte, error) {
	var args [][]byte
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		var cur []byte
		switch line[i] {
		case '"':
			i++
			closed := false
			for i < len(line) {
				c := line[i]
				if c == '\\' && i+1 < len(line) {
					i++
					switch line[i] {
					case 'n':
						cur = append(cur, '\n')
					case 'r':
						cur = append(cur, '\r')
					case 't':
						cur = append(cur, '\t')
					case 'x':
						if i+2 < len(line) {
							if v, err := strconv.ParseUint(line[i+1:i+3], 16, 8); err == nil {
								cur = append(cur, byte(v))
								i += 2
								break
							}
						}
						cur = append(cur, 'x')
					default:
						cur = append(cur, line[i])
					}
					i++
					continue
				}
				if c == '"' {
					closed = true
					i++
					break
				}
				cur = append(cur, c)
				i++
			}
			if !closed || (i < len(line) && line[i] != ' ' && line[i] != '\t') {
				return nil, &ProtocolError{Msg: "unbalanced quotes in request"}
			}
		case '\'':
			i++
			closed := false
			for i < len(line) {
				c := line[i]
				if c == '\\' && i+1 < len(line) && line[i+1] == '\'' {
					cur = append(cur, '\'')
					i += 2
					continue
				}
				if c == '\'' {
					closed = true
					i++
					break
				}
				cur = append(cur, c)
				i++
			}
			if !closed || (i < len(line) && line[i] != ' ' && line[i] != '\t') {
				return nil, &ProtocolError{Msg: "unbalanced quotes in request"}
			}
		default:
			for i < len(line) && line[i] != ' ' && line[i] != '\t' {
				cur = append(cur, line[i])
				i++
			}
		}
		args = append(args, cur)
	}
	return args, nil
}

// Encode записывает значение в w в формате RESP2.
func Encode(w io.Writer, v Value) error {
	var buf bytes.Buffer
	encode(&buf, v)
	_, err := w.Write(buf.Bytes())
	return err
}

// Marshal возвращает RESP-представление значения.
func Marshal(v Value) []byte {
	var buf bytes.Buffer
	encode(&buf, v)
	return buf.Bytes()
}

func encode(b *bytes.Buffer, v Value) {
	switch v.Kind {
	case Simple:
		b.WriteByte('+')
		b.Write(sanitize(v.Str))
		b.WriteString("\r\n")
	case Error:
		b.WriteByte('-')
		b.Write(sanitize(v.Str))
		b.WriteString("\r\n")
	case Integer:
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(v.N, 10))
		b.WriteString("\r\n")
	case Bulk:
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(len(v.Str)))
		b.WriteString("\r\n")
		b.Write(v.Str)
		b.WriteString("\r\n")
	case Nil:
		b.WriteString("$-1\r\n")
	case NilArray:
		b.WriteString("*-1\r\n")
	case Array:
		b.WriteByte('*')
		b.WriteString(strconv.Itoa(len(v.Arr)))
		b.WriteString("\r\n")
		for _, e := range v.Arr {
			encode(b, e)
		}
	}
}

// sanitize убирает переводы строк: в простых строках и ошибках они сломали бы протокол.
func sanitize(s []byte) []byte {
	if !bytes.ContainsAny(s, "\r\n") {
		return s
	}
	out := bytes.ReplaceAll(s, []byte("\r"), []byte(" "))
	return bytes.ReplaceAll(out, []byte("\n"), []byte(" "))
}
