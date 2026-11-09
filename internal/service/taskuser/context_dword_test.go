package taskuser

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestScalarQueriesUseExactDWORDContract(t *testing.T) {
	badLength := errors.New("native ERROR_BAD_LENGTH")
	value, err := queryDWORD(func(buffer []byte) (uint32, error) {
		if len(buffer) != 4 {
			return 4, badLength
		}
		binary.LittleEndian.PutUint32(buffer, 0x12345678)
		return 4, nil
	})
	if err != nil || value != 0x12345678 {
		t.Fatalf("exact native DWORD query: value=%x err=%v", value, err)
	}
	for _, returned := range []uint32{0, 3, 5, 64} {
		if _, err := queryDWORD(func([]byte) (uint32, error) { return returned, nil }); !errors.Is(err, ErrIdentity) {
			t.Fatalf("accepted invalid native scalar response length %d: %v", returned, err)
		}
	}
	if _, err := queryDWORD(func([]byte) (uint32, error) { return 4, badLength }); !errors.Is(err, badLength) {
		t.Fatal("native scalar error was not preserved", err)
	}
}
