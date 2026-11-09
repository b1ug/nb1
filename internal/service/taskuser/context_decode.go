package taskuser

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var errBufferSmall = errors.New("token information buffer too small")

// The scalar token classes require their exact DWORD size. Native Windows
// returns ERROR_BAD_LENGTH for TokenElevation with the generic 64-byte buffer.
// A scalar response must never enter the variable-length allocation loop.
func queryDWORD(query func([]byte) (uint32, error)) (uint32, error) {
	buffer := make([]byte, 4)
	n, err := query(buffer)
	if err != nil {
		return 0, err
	}
	if n != 4 {
		return 0, rejected("native token DWORD response length is invalid")
	}
	return decodeDWORD(buffer)
}

// queryInfo bounds allocation and retries even if the native size changes.
func queryInfo(query func([]byte) (uint32, error)) ([]byte, error) {
	size := uint32(64)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]byte, size)
		n, err := query(buffer)
		if err == nil {
			if n == 0 || n > size {
				return nil, rejected("native token response length is invalid")
			}
			return buffer[:n], nil
		}
		if !errors.Is(err, errBufferSmall) {
			return nil, err
		}
		if n <= size || n > 64<<10 {
			return nil, rejected("native token buffer growth is invalid or exceeds 64 KiB")
		}
		size = n
	}
	return nil, rejected("native token size did not stabilize in four queries")
}

type tokenLayout struct{ pointerSize, attributesSize, groupsOffset int }

func (l tokenLayout) valid() bool {
	return (l.pointerSize == 4 && l.attributesSize == 8 && l.groupsOffset == 4) ||
		(l.pointerSize == 8 && l.attributesSize == 16 && l.groupsOffset == 8)
}

// sidAt reads a pointer value but never dereferences it. The SID must be wholly
// inside the returned buffer, after its enclosing native structure/array.
func sidAt(buffer []byte, base uint64, pointerOffset, minimumSIDOffset int, layout tokenLayout) (string, error) {
	if !layout.valid() || pointerOffset < 0 || pointerOffset > len(buffer)-layout.pointerSize {
		return "", rejected("token SID pointer structure is truncated")
	}
	var pointer uint64
	if layout.pointerSize == 4 {
		pointer = uint64(binary.LittleEndian.Uint32(buffer[pointerOffset:]))
	} else {
		pointer = binary.LittleEndian.Uint64(buffer[pointerOffset:])
	}
	if pointer < base {
		return "", rejected("token SID pointer precedes its response buffer")
	}
	offset := pointer - base
	if offset > uint64(len(buffer)) || offset < uint64(minimumSIDOffset) || uint64(len(buffer))-offset < 8 {
		return "", rejected("token SID pointer is outside its response payload")
	}
	sid := buffer[int(offset):]
	count := int(sid[1])
	if sid[0] != 1 || count < 1 || count > 15 || len(sid) < 8+4*count {
		return "", rejected("token SID is truncated or has an unknown revision/count")
	}
	var authority uint64
	for _, value := range sid[2:8] {
		authority = authority<<8 | uint64(value)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "S-1-%d", authority)
	for i := 0; i < count; i++ {
		text.WriteByte('-')
		text.WriteString(strconv.FormatUint(uint64(binary.LittleEndian.Uint32(sid[8+4*i:])), 10))
	}
	return text.String(), nil
}

func decodeLabel(buffer []byte, base uint64, layout tokenLayout, integrity bool) (string, error) {
	if !layout.valid() || len(buffer) < layout.attributesSize {
		return "", rejected("token SID_AND_ATTRIBUTES is truncated")
	}
	if integrity && binary.LittleEndian.Uint32(buffer[layout.pointerSize:])&0x20 == 0 {
		return "", rejected("mandatory label is not marked SE_GROUP_INTEGRITY")
	}
	return sidAt(buffer, base, 0, layout.attributesSize, layout)
}

func decodeGroups(buffer []byte, base uint64, layout tokenLayout) ([]tokenGroup, error) {
	if !layout.valid() || len(buffer) < layout.groupsOffset {
		return nil, rejected("TOKEN_GROUPS header is truncated")
	}
	count := uint64(binary.LittleEndian.Uint32(buffer))
	arrayEnd := uint64(layout.groupsOffset) + count*uint64(layout.attributesSize)
	if arrayEnd > uint64(len(buffer)) {
		return nil, rejected("TOKEN_GROUPS array exceeds its response")
	}
	groups := make([]tokenGroup, 0, int(count))
	for i := 0; i < int(count); i++ {
		offset := layout.groupsOffset + i*layout.attributesSize
		sid, err := sidAt(buffer, base, offset, int(arrayEnd), layout)
		if err != nil {
			return nil, err
		}
		groups = append(groups, tokenGroup{sid, binary.LittleEndian.Uint32(buffer[offset+layout.pointerSize:])})
	}
	return groups, nil
}

func decodeDWORD(buffer []byte) (uint32, error) {
	if len(buffer) != 4 {
		return 0, rejected("token DWORD response must contain exactly four bytes")
	}
	return binary.LittleEndian.Uint32(buffer), nil
}
