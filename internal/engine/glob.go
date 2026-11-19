package engine

// globMatch реализует шаблоны Redis для KEYS: * ? [abc] [a-z] [^a] и экранирование \.
func globMatch(pattern, s string) bool {
	return glob([]byte(pattern), []byte(s))
}

func glob(p, s []byte) bool {
	for len(p) > 0 {
		switch p[0] {
		case '*':
			for len(p) > 1 && p[1] == '*' { // подряд идущие * эквивалентны одному
				p = p[1:]
			}
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if glob(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if len(s) == 0 {
				return false
			}
			s = s[1:]
			p = p[1:]
		case '[':
			if len(s) == 0 {
				return false
			}
			end := 1
			neg := false
			if end < len(p) && p[end] == '^' {
				neg = true
				end++
			}
			matched := false
			for end < len(p) && p[end] != ']' {
				switch {
				case p[end] == '\\' && end+1 < len(p):
					end++
					if p[end] == s[0] {
						matched = true
					}
				case end+2 < len(p) && p[end+1] == '-' && p[end+2] != ']':
					lo, hi := p[end], p[end+2]
					if lo > hi {
						lo, hi = hi, lo
					}
					if s[0] >= lo && s[0] <= hi {
						matched = true
					}
					end += 2
				default:
					if p[end] == s[0] {
						matched = true
					}
				}
				end++
			}
			if matched == neg {
				return false
			}
			s = s[1:]
			if end < len(p) {
				p = p[end+1:] // пропускаем закрывающую ]
			} else {
				p = p[end:] // незакрытый класс трактуется как закрытый в конце шаблона
			}
		case '\\':
			if len(p) >= 2 {
				p = p[1:]
			}
			fallthrough
		default:
			if len(s) == 0 || p[0] != s[0] {
				return false
			}
			s = s[1:]
			p = p[1:]
		}
	}
	return len(s) == 0
}
