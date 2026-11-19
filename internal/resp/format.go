package resp

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatCLI печатает значение так, как это делает redis-cli в интерактивном режиме.
func FormatCLI(v Value) string {
	return formatCLI(v, 0)
}

func formatCLI(v Value, indent int) string {
	switch v.Kind {
	case Simple:
		return string(v.Str)
	case Error:
		return "(error) " + string(v.Str)
	case Integer:
		return "(integer) " + strconv.FormatInt(v.N, 10)
	case Bulk:
		return quote(v.Str)
	case Nil, NilArray:
		return "(nil)"
	case Array:
		if len(v.Arr) == 0 {
			return "(empty array)"
		}
		width := len(strconv.Itoa(len(v.Arr)))
		pad := strings.Repeat(" ", indent)
		var lines []string
		for i, e := range v.Arr {
			prefix := fmt.Sprintf("%*d) ", width, i+1)
			body := formatCLI(e, indent+width+2)
			if i == 0 {
				lines = append(lines, prefix+body)
			} else {
				lines = append(lines, pad+prefix+body)
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}

// quote экранирует непечатаемые байты, как redis-cli: \n, \r, \t, \xNN.
func quote(b []byte) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range b {
		switch c {
		case '\\', '"':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&sb, `\x%02x`, c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
