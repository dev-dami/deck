package passkey

import (
	"encoding/binary"
	"errors"
	"math"
)

// A deliberately small CBOR reader: just what a WebAuthn attestation object
// and a COSE key use (integers, byte and text strings, arrays, maps, and the
// simple values). No tags, floats or indefinite lengths; those are errors.

var errCBOR = errors.New("passkey: malformed CBOR")

const (
	maxDepth = 8
	maxItems = 64
)

type cborValue struct {
	kind  byte // 0 uint, 1 negint, 2 bytes, 3 text, 4 array, 5 map, 7 simple
	n     int64
	b     []byte
	s     string
	arr   []cborValue
	pairs []cborPair
	simpl byte
}

type cborPair struct{ k, v cborValue }

func decodeCBOR(data []byte) (cborValue, []byte, error) { return readCBOR(data, 0) }

func readCBOR(data []byte, depth int) (cborValue, []byte, error) {
	if depth > maxDepth || len(data) == 0 {
		return cborValue{}, nil, errCBOR
	}
	major, info := data[0]>>5, data[0]&0x1f
	data = data[1:]

	var arg uint64
	switch {
	case info < 24:
		arg = uint64(info)
	case info == 24:
		if len(data) < 1 {
			return cborValue{}, nil, errCBOR
		}
		arg, data = uint64(data[0]), data[1:]
	case info == 25:
		if len(data) < 2 {
			return cborValue{}, nil, errCBOR
		}
		arg, data = uint64(binary.BigEndian.Uint16(data)), data[2:]
	case info == 26:
		if len(data) < 4 {
			return cborValue{}, nil, errCBOR
		}
		arg, data = uint64(binary.BigEndian.Uint32(data)), data[4:]
	case info == 27:
		if len(data) < 8 {
			return cborValue{}, nil, errCBOR
		}
		arg, data = binary.BigEndian.Uint64(data), data[8:]
	default:
		return cborValue{}, nil, errCBOR // indefinite length or reserved
	}

	v := cborValue{kind: major}
	switch major {
	case 0:
		if arg > math.MaxInt64 {
			return cborValue{}, nil, errCBOR
		}
		v.n = int64(arg)
	case 1:
		if arg > math.MaxInt64 {
			return cborValue{}, nil, errCBOR
		}
		v.n = -1 - int64(arg)
	case 2, 3:
		if arg > uint64(len(data)) {
			return cborValue{}, nil, errCBOR
		}
		raw := data[:arg]
		data = data[arg:]
		if major == 2 {
			v.b = raw
		} else {
			v.s = string(raw)
		}
	case 4:
		if arg > maxItems {
			return cborValue{}, nil, errCBOR
		}
		for i := uint64(0); i < arg; i++ {
			var item cborValue
			var err error
			if item, data, err = readCBOR(data, depth+1); err != nil {
				return cborValue{}, nil, err
			}
			v.arr = append(v.arr, item)
		}
	case 5:
		if arg > maxItems {
			return cborValue{}, nil, errCBOR
		}
		for i := uint64(0); i < arg; i++ {
			var k, val cborValue
			var err error
			if k, data, err = readCBOR(data, depth+1); err != nil {
				return cborValue{}, nil, err
			}
			if val, data, err = readCBOR(data, depth+1); err != nil {
				return cborValue{}, nil, err
			}
			v.pairs = append(v.pairs, cborPair{k, val})
		}
	case 7:
		if info < 20 || info > 22 {
			return cborValue{}, nil, errCBOR
		}
		v.simpl = info
	default:
		return cborValue{}, nil, errCBOR
	}
	return v, data, nil
}

// get finds a map value by integer key.
func (v cborValue) getInt(key int64) (cborValue, bool) {
	for _, p := range v.pairs {
		if (p.k.kind == 0 || p.k.kind == 1) && p.k.n == key {
			return p.v, true
		}
	}
	return cborValue{}, false
}

// getText finds a map value by text key.
func (v cborValue) getText(key string) (cborValue, bool) {
	for _, p := range v.pairs {
		if p.k.kind == 3 && p.k.s == key {
			return p.v, true
		}
	}
	return cborValue{}, false
}
