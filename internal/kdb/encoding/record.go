package encoding

// Encoder writes a schema v1 record.
//
// Record format (PRD §4.14):
//
//	[version:1][field_count:1][tag:1 data:...]×N
//
// Fields are appended in order. Call Bytes to finalise the record.
// The returned slice shares memory with the Encoder; clone before Reset.
type Encoder struct {
	buf        []byte
	fieldCount uint8
}

// NewEncoder creates a new Encoder for the given schema version.
func NewEncoder(version uint8) *Encoder {
	e := &Encoder{buf: make([]byte, 2, 64)}
	e.buf[0] = version
	return e
}

// PutUint64 appends a uint64 field (WireFixed64).
func (e *Encoder) PutUint64(fieldID uint8, v uint64) {
	e.buf = append(e.buf, MakeTag(fieldID, WireFixed64))
	e.buf = appendFixed64(e.buf, v)
	e.fieldCount++
}

// PutInt64 appends an int64 field (WireFixed64).
func (e *Encoder) PutInt64(fieldID uint8, v int64) {
	e.buf = append(e.buf, MakeTag(fieldID, WireFixed64))
	e.buf = appendFixed64(e.buf, uint64(v)) //nolint:gosec // G115: bit reinterpretation, not arithmetic.
	e.fieldCount++
}

// PutFloat64 appends a float64 field (WireFixed64).
func (e *Encoder) PutFloat64(fieldID uint8, v float64) {
	e.buf = append(e.buf, MakeTag(fieldID, WireFixed64))
	var b [8]byte
	PutFloat64(b[:], v)
	e.buf = append(e.buf, b[:]...)
	e.fieldCount++
}

// PutTimestamp appends a timestamp field as int64 unix nanos (WireFixed64).
func (e *Encoder) PutTimestamp(fieldID uint8, v int64) {
	e.PutInt64(fieldID, v)
}

// PutString appends a string field (WireBytes).
func (e *Encoder) PutString(fieldID uint8, v string) {
	e.buf = append(e.buf, MakeTag(fieldID, WireBytes))
	e.buf = AppendUvarint(e.buf, uint64(len(v)))
	e.buf = append(e.buf, v...)
	e.fieldCount++
}

// PutBytes appends a []byte field (WireBytes).
func (e *Encoder) PutBytes(fieldID uint8, v []byte) {
	e.buf = append(e.buf, MakeTag(fieldID, WireBytes))
	e.buf = AppendUvarint(e.buf, uint64(len(v)))
	e.buf = append(e.buf, v...)
	e.fieldCount++
}

// PutBool appends a bool field (WireFixed8).
func (e *Encoder) PutBool(fieldID uint8, v bool) {
	e.buf = append(e.buf, MakeTag(fieldID, WireFixed8))
	if v {
		e.buf = append(e.buf, 1)
	} else {
		e.buf = append(e.buf, 0)
	}
	e.fieldCount++
}

// PutStringList appends a list<string> field (WireList).
func (e *Encoder) PutStringList(fieldID uint8, v []string) {
	e.buf = append(e.buf, MakeTag(fieldID, WireList))
	e.buf = AppendUvarint(e.buf, uint64(len(v)))
	for _, s := range v {
		e.buf = AppendUvarint(e.buf, uint64(len(s)))
		e.buf = append(e.buf, s...)
	}
	e.fieldCount++
}

// Bytes finalises the record and returns the encoded bytes.
// The returned slice shares memory with the Encoder; clone before Reset.
func (e *Encoder) Bytes() []byte {
	e.buf[1] = e.fieldCount
	return e.buf
}

// Reset resets the encoder for reuse with a new version.
func (e *Encoder) Reset(version uint8) {
	e.buf = e.buf[:2]
	e.buf[0] = version
	e.buf[1] = 0
	e.fieldCount = 0
}

func appendFixed64(b []byte, v uint64) []byte {
	var tmp [8]byte
	PutUint64(tmp[:], v)
	return append(b, tmp[:]...)
}

// Decoder reads fields from a schema v1 encoded record.
type Decoder struct {
	data       []byte
	pos        int
	version    uint8
	fieldCount uint8
	fieldsRead uint8
}

// NewDecoder creates a Decoder from the given record bytes.
// It reads the version and field count header.
func NewDecoder(data []byte) (*Decoder, error) {
	if len(data) < 2 {
		return nil, ErrUnexpectedEOF
	}
	return &Decoder{
		data:       data,
		pos:        2,
		version:    data[0],
		fieldCount: data[1],
	}, nil
}

// Version returns the schema version of the record.
func (d *Decoder) Version() uint8 {
	return d.version
}

// FieldCount returns the number of fields in the record.
func (d *Decoder) FieldCount() uint8 {
	return d.fieldCount
}

// Done returns true if all declared fields have been consumed.
func (d *Decoder) Done() bool {
	return d.fieldsRead >= d.fieldCount
}

// Next reads the next field tag and returns its field ID and wire type.
// Call the appropriate typed reader (Uint64, String, etc.) or Skip after Next.
func (d *Decoder) Next() (fieldID uint8, wt WireType, err error) {
	if d.fieldsRead >= d.fieldCount {
		return 0, 0, ErrNoFieldsRemaining
	}
	if d.pos >= len(d.data) {
		return 0, 0, ErrUnexpectedEOF
	}
	tag := d.data[d.pos]
	d.pos++
	fieldID, wt = ParseTag(tag)
	if wt > maxWireType {
		return 0, 0, ErrInvalidWireType
	}
	d.fieldsRead++
	return fieldID, wt, nil
}

// Uint64 reads a WireFixed64 value as uint64.
func (d *Decoder) Uint64() (uint64, error) {
	if d.pos+8 > len(d.data) {
		return 0, ErrUnexpectedEOF
	}
	v := Uint64(d.data[d.pos:])
	d.pos += 8
	return v, nil
}

// Int64 reads a WireFixed64 value as int64.
func (d *Decoder) Int64() (int64, error) {
	if d.pos+8 > len(d.data) {
		return 0, ErrUnexpectedEOF
	}
	v := Int64(d.data[d.pos:])
	d.pos += 8
	return v, nil
}

// Float64 reads a WireFixed64 value as float64.
func (d *Decoder) Float64() (float64, error) {
	if d.pos+8 > len(d.data) {
		return 0, ErrUnexpectedEOF
	}
	v := Float64(d.data[d.pos:])
	d.pos += 8
	return v, nil
}

// Timestamp reads a WireFixed64 value as a timestamp (int64 unix nanos).
func (d *Decoder) Timestamp() (int64, error) {
	return d.Int64()
}

// String reads a WireBytes value as a string.
func (d *Decoder) String() (string, error) {
	b, err := d.Bytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Bytes reads a WireBytes value as a byte slice.
// The returned slice points into the decoder's buffer; clone if needed.
func (d *Decoder) Bytes() ([]byte, error) {
	if d.pos >= len(d.data) {
		return nil, ErrUnexpectedEOF
	}
	length, n := Uvarint(d.data[d.pos:])
	if n <= 0 {
		if n == 0 {
			return nil, ErrUnexpectedEOF
		}
		return nil, ErrVarintOverflow
	}
	d.pos += n
	end := d.pos + int(length) //nolint:gosec // G115: overflow guarded by end < d.pos check.
	if end > len(d.data) || end < d.pos {
		return nil, ErrUnexpectedEOF
	}
	v := d.data[d.pos:end]
	d.pos = end
	return v, nil
}

// Bool reads a WireFixed8 value as bool.
func (d *Decoder) Bool() (bool, error) {
	if d.pos >= len(d.data) {
		return false, ErrUnexpectedEOF
	}
	v := d.data[d.pos] != 0
	d.pos++
	return v, nil
}

// StringList reads a WireList value as []string.
func (d *Decoder) StringList() ([]string, error) {
	if d.pos >= len(d.data) {
		return nil, ErrUnexpectedEOF
	}
	count, n := Uvarint(d.data[d.pos:])
	if n <= 0 {
		if n == 0 {
			return nil, ErrUnexpectedEOF
		}
		return nil, ErrVarintOverflow
	}
	d.pos += n

	// Cap pre-allocation to avoid OOM on malformed data.
	prealloc := count
	if prealloc > 256 {
		prealloc = 256
	}
	result := make([]string, 0, prealloc)
	for range count {
		length, ln := Uvarint(d.data[d.pos:])
		if ln <= 0 {
			if ln == 0 {
				return nil, ErrUnexpectedEOF
			}
			return nil, ErrVarintOverflow
		}
		d.pos += ln
		end := d.pos + int(length) //nolint:gosec // G115: overflow guarded by end < d.pos check.
		if end > len(d.data) || end < d.pos {
			return nil, ErrUnexpectedEOF
		}
		result = append(result, string(d.data[d.pos:end]))
		d.pos = end
	}
	return result, nil
}

// Skip advances past the current field data without reading it.
// This supports forward compatibility: unknown fields can be skipped.
func (d *Decoder) Skip(wt WireType) error {
	switch wt {
	case WireFixed64:
		if d.pos+8 > len(d.data) {
			return ErrUnexpectedEOF
		}
		d.pos += 8
	case WireFixed8:
		if d.pos >= len(d.data) {
			return ErrUnexpectedEOF
		}
		d.pos++
	case WireBytes:
		return d.skipBytes()
	case WireList:
		return d.skipList()
	default:
		return ErrInvalidWireType
	}
	return nil
}

func (d *Decoder) skipBytes() error {
	if d.pos >= len(d.data) {
		return ErrUnexpectedEOF
	}
	length, n := Uvarint(d.data[d.pos:])
	if n <= 0 {
		if n == 0 {
			return ErrUnexpectedEOF
		}
		return ErrVarintOverflow
	}
	d.pos += n
	end := d.pos + int(length) //nolint:gosec // G115: overflow guarded by end < d.pos check.
	if end > len(d.data) || end < d.pos {
		return ErrUnexpectedEOF
	}
	d.pos = end
	return nil
}

func (d *Decoder) skipList() error {
	if d.pos >= len(d.data) {
		return ErrUnexpectedEOF
	}
	count, n := Uvarint(d.data[d.pos:])
	if n <= 0 {
		if n == 0 {
			return ErrUnexpectedEOF
		}
		return ErrVarintOverflow
	}
	d.pos += n
	for range count {
		if err := d.skipBytes(); err != nil {
			return err
		}
	}
	return nil
}
