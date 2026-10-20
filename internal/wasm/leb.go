package wasm

func appendULEB(b []byte, v uint64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		b = append(b, c)
		if v == 0 {
			return b
		}
	}
}

func appendSLEB(b []byte, v int64) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		done := (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0)
		if !done {
			c |= 0x80
		}
		b = append(b, c)
		if done {
			return b
		}
	}
}

func appendName(b []byte, s string) []byte {
	b = appendULEB(b, uint64(len(s)))
	return append(b, s...)
}
