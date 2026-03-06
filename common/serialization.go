package common

import (
	"bytes"
	"encoding/gob"
)

// EncodeToBytes serializes value into a byte slice using gob.
func EncodeToBytes(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeFromBytes deserializes a gob-encoded byte slice into out.
func DecodeFromBytes(data []byte, out interface{}) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	return dec.Decode(out)
}
