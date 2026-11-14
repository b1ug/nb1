package taskuser

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

const fakeBase = uint64(0x10000)

func binarySID(text string) []byte {
	parts, err := sidParts(text)
	if err != nil {
		panic(err)
	}
	b := make([]byte, 8+4*(len(parts)-1))
	b[0] = 1
	b[1] = byte(len(parts) - 1)
	authority := parts[0]
	for i := 7; i >= 2; i-- {
		b[i] = byte(authority)
		authority >>= 8
	}
	for i, value := range parts[1:] {
		binary.LittleEndian.PutUint32(b[8+4*i:], uint32(value))
	}
	return b
}

func putPointer(b []byte, offset int, value uint64, l tokenLayout) {
	if l.pointerSize == 4 {
		binary.LittleEndian.PutUint32(b[offset:], uint32(value))
	} else {
		binary.LittleEndian.PutUint64(b[offset:], value)
	}
}

func labelPacket(sid string, attributes uint32, l tokenLayout) []byte {
	s := binarySID(sid)
	b := make([]byte, l.attributesSize+len(s))
	copy(b[l.attributesSize:], s)
	putPointer(b, 0, fakeBase+uint64(l.attributesSize), l)
	binary.LittleEndian.PutUint32(b[l.pointerSize:], attributes)
	return b
}

func groupPacket(groups []tokenGroup, l tokenLayout) []byte {
	end := l.groupsOffset + len(groups)*l.attributesSize
	b := make([]byte, end)
	binary.LittleEndian.PutUint32(b, uint32(len(groups)))
	for i, group := range groups {
		offset := l.groupsOffset + i*l.attributesSize
		putPointer(b, offset, fakeBase+uint64(len(b)), l)
		binary.LittleEndian.PutUint32(b[offset+l.pointerSize:], group.attributes)
		b = append(b, binarySID(group.sid)...)
	}
	return b
}

func TestTokenStructuresFor32And64BitLayouts(t *testing.T) {
	for _, layout := range []tokenLayout{{4, 8, 4}, {8, 16, 8}} {
		for _, sid := range []string{"S-1-5-21-1-2-3-1001", "S-1-12-1-4294967295-2-3-500", "S-1-16-8192"} {
			b := labelPacket(sid, 0x20, layout)
			got, err := decodeLabel(b, fakeBase, layout, sid == "S-1-16-8192")
			if err != nil || got != sid {
				t.Fatalf("layout=%+v sid=%s got=%s err=%v", layout, sid, got, err)
			}
		}
		want := []tokenGroup{{"S-1-5-4", 7}, {"S-1-5-32-544", 0x10}}
		got, err := decodeGroups(groupPacket(want, layout), fakeBase, layout)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("layout=%+v groups=%+v err=%v", layout, got, err)
		}
	}
}

func TestMalformedSIDPointersAndLabelsRejected(t *testing.T) {
	for _, l := range []tokenLayout{{4, 8, 4}, {8, 16, 8}} {
		cases := map[string]func([]byte) []byte{
			"short header":                func(b []byte) []byte { return b[:l.attributesSize-1] },
			"null":                        func(b []byte) []byte { putPointer(b, 0, 0, l); return b },
			"before base":                 func(b []byte) []byte { putPointer(b, 0, fakeBase-1, l); return b },
			"after payload":               func(b []byte) []byte { putPointer(b, 0, fakeBase+uint64(len(b))+1, l); return b },
			"inside header":               func(b []byte) []byte { putPointer(b, 0, fakeBase+1, l); return b },
			"unknown revision":            func(b []byte) []byte { b[l.attributesSize] = 2; return b },
			"zero subauthorities":         func(b []byte) []byte { b[l.attributesSize+1] = 0; return b },
			"too many subauthorities":     func(b []byte) []byte { b[l.attributesSize+1] = 16; return b },
			"truncated SID":               func(b []byte) []byte { return b[:len(b)-1] },
			"missing integrity attribute": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[l.pointerSize:], 0); return b },
		}
		for name, mutate := range cases {
			t.Run(name+string(rune('0'+l.pointerSize)), func(t *testing.T) {
				b := mutate(labelPacket("S-1-16-8192", 0x20, l))
				if _, err := decodeLabel(b, fakeBase, l, true); err == nil {
					t.Fatal("accepted malformed label")
				}
			})
		}
		if _, err := decodeLabel(labelPacket("S-1-16-8192", 0x20, l), fakeBase, tokenLayout{}, true); err == nil {
			t.Fatal("unknown ABI accepted")
		}
	}
}

func TestMalformedGroupArrayRejected(t *testing.T) {
	for _, l := range []tokenLayout{{4, 8, 4}, {8, 16, 8}} {
		valid := func() []byte { return groupPacket([]tokenGroup{{"S-1-5-4", 7}}, l) }
		b := valid()
		binary.LittleEndian.PutUint32(b, ^uint32(0))
		if _, err := decodeGroups(b, fakeBase, l); err == nil {
			t.Fatal("oversized array accepted")
		}
		b = valid()
		putPointer(b, l.groupsOffset, fakeBase+uint64(l.groupsOffset), l)
		if _, err := decodeGroups(b, fakeBase, l); err == nil {
			t.Fatal("SID aliasing array accepted")
		}
		if _, err := decodeGroups(valid()[:l.groupsOffset-1], fakeBase, l); err == nil {
			t.Fatal("short groups header accepted")
		}
	}
}

func TestQueryInfoBoundedGrowthAndErrorHandling(t *testing.T) {
	for _, kind := range []string{"fixed success", "one growth", "native failure", "zero length", "length over capacity", "no growth", "over limit", "retry limit"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			nativeErr := errors.New("native error")
			b, err := queryInfo(func(b []byte) (uint32, error) {
				calls++
				switch kind {
				case "one growth":
					if calls == 1 {
						return 128, errBufferSmall
					}
				case "native failure":
					return 0, nativeErr
				case "zero length":
					return 0, nil
				case "length over capacity":
					return uint32(len(b) + 1), nil
				case "no growth":
					return uint32(len(b)), errBufferSmall
				case "over limit":
					return 65537, errBufferSmall
				case "retry limit":
					return uint32(len(b) * 2), errBufferSmall
				}
				binary.LittleEndian.PutUint32(b, 3)
				return 4, nil
			})
			if kind == "fixed success" || kind == "one growth" {
				v, decodeErr := decodeDWORD(b)
				if err != nil || decodeErr != nil || v != 3 {
					t.Fatalf("b=%v err=%v", b, err)
				}
			} else if err == nil {
				t.Fatal("accepted invalid query result")
			}
			if kind == "native failure" && !errors.Is(err, nativeErr) {
				t.Fatal("native error lost")
			}
			if kind == "retry limit" && calls != 4 {
				t.Fatal(calls)
			}
			if calls > 4 {
				t.Fatal("retry bound exceeded")
			}
		})
	}
	for _, n := range []int{0, 3, 5, 64} {
		if _, err := decodeDWORD(make([]byte, n)); err == nil {
			t.Fatalf("accepted DWORD length %d", n)
		}
	}
}
