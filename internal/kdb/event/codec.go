package event

import (
	"fmt"
	"time"

	"github.com/jenaiz/pcke/internal/kdb/encoding"
)

// Encode serialises an event into a value blob suitable for storage.
//
// The output begins with the kdb encoding header (version + field count)
// followed by header fields in numerical id order, then the kind-specific
// payload fields. Empty payload fields are omitted.
//
// Encode does not validate consistency between the event's Kind() and
// its concrete type; callers should not construct mismatched events.
func Encode(e Event) ([]byte, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: nil event", ErrCorrupt)
	}
	if e.ID() == "" {
		return nil, ErrEmptyID
	}
	hdr := e.Header()
	if hdr.Lifecycle != 0 && !hdr.Lifecycle.Valid() {
		return nil, ErrInvalidLifecycle
	}
	if !e.Kind().Valid() {
		return nil, ErrInvalidKind
	}

	enc := encoding.NewEncoder(recordSchemaVersion)
	enc.PutUint64(fieldVersion, hdr.Version)
	enc.PutTimestamp(fieldCreatedAtNanos, hdr.CreatedAt.UnixNano())
	if len(hdr.Supersedes) > 0 {
		enc.PutBytes(fieldSupersedesKey, hdr.Supersedes)
	}
	enc.PutUint8(fieldKind, uint8(e.Kind()))
	if hdr.Lifecycle != 0 {
		enc.PutUint8(fieldLifecycle, uint8(hdr.Lifecycle))
	}
	e.encodePayload(enc)

	// Clone: encoder.Bytes() shares memory with the encoder's internal buffer.
	out := enc.Bytes()
	cloned := make([]byte, len(out))
	copy(cloned, out)
	return cloned, nil
}

// Decode parses a value blob into the matching concrete event type.
//
// The id argument is the logical (unescaped) id from the key — codec
// values do not carry the id, so the caller supplies it from ParseKey.
//
// Returns ErrSchemaVersion if the record version is unknown,
// ErrInvalidKind if the event_kind field is missing or unrecognised,
// or ErrCorrupt wrapping the underlying decoder error for any malformed
// field.
func Decode(value []byte, id string) (Event, error) {
	dec, err := encoding.NewDecoder(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrCorrupt, err)
	}
	if dec.Version() != recordSchemaVersion {
		return nil, fmt.Errorf("%w: got %d want %d", ErrSchemaVersion, dec.Version(), recordSchemaVersion)
	}

	hdr, kind, payloadFields, err := readRecord(dec)
	if err != nil {
		return nil, err
	}
	if !kind.Valid() {
		return nil, fmt.Errorf("%w: missing event_kind field", ErrInvalidKind)
	}
	if id == "" {
		return nil, ErrEmptyID
	}
	return assemble(kind, id, hdr, payloadFields)
}

// readRecord walks the decoder once, populating header fields directly and
// collecting kind-specific fields into payload for later dispatch.
func readRecord(dec *encoding.Decoder) (Header, Kind, []rawPayload, error) {
	var (
		hdr     Header
		kind    Kind
		payload []rawPayload
	)
	for !dec.Done() {
		fid, wt, err := dec.Next()
		if err != nil {
			return hdr, kind, nil, fmt.Errorf("%w: %s", ErrCorrupt, err)
		}
		applied, err := applyHeaderField(dec, fid, &hdr, &kind)
		if err != nil {
			return hdr, kind, nil, err
		}
		if applied {
			continue
		}
		f, err := captureRaw(dec, fid, wt)
		if err != nil {
			return hdr, kind, nil, err
		}
		payload = append(payload, f)
	}
	return hdr, kind, payload, nil
}

// applyHeaderField reads a header-class field from dec into hdr or kind.
// Returns ok=true if the field id was a header field (consumed from dec);
// false means the caller should treat it as payload.
func applyHeaderField(dec *encoding.Decoder, fid uint8, hdr *Header, kind *Kind) (bool, error) {
	switch fid {
	case fieldVersion:
		v, err := dec.Uint64()
		if err != nil {
			return true, fmt.Errorf("%w: version: %s", ErrCorrupt, err)
		}
		hdr.Version = v
	case fieldCreatedAtNanos:
		ns, err := dec.Int64()
		if err != nil {
			return true, fmt.Errorf("%w: created_at: %s", ErrCorrupt, err)
		}
		if ns == 0 {
			hdr.CreatedAt = time.Time{}
		} else {
			hdr.CreatedAt = time.Unix(0, ns).UTC()
		}
	case fieldSupersedesKey:
		b, err := dec.Bytes()
		if err != nil {
			return true, fmt.Errorf("%w: supersedes: %s", ErrCorrupt, err)
		}
		if len(b) > 0 {
			hdr.Supersedes = append([]byte(nil), b...)
		}
	case fieldKind:
		u, err := dec.Uint8()
		if err != nil {
			return true, fmt.Errorf("%w: event_kind: %s", ErrCorrupt, err)
		}
		k := Kind(u)
		if !k.Valid() {
			return true, fmt.Errorf("%w: %d", ErrInvalidKind, u)
		}
		*kind = k
	case fieldLifecycle:
		u, err := dec.Uint8()
		if err != nil {
			return true, fmt.Errorf("%w: lifecycle: %s", ErrCorrupt, err)
		}
		lc := Lifecycle(u)
		if !lc.Valid() {
			return true, fmt.Errorf("%w: %d", ErrInvalidLifecycle, u)
		}
		hdr.Lifecycle = lc
	default:
		return false, nil
	}
	return true, nil
}

func captureRaw(dec *encoding.Decoder, fid uint8, wt encoding.WireType) (rawPayload, error) {
	switch wt {
	case encoding.WireBytes:
		s, err := dec.String()
		if err != nil {
			return rawPayload{}, fmt.Errorf("%w: field %d: %s", ErrCorrupt, fid, err)
		}
		return rawPayload{id: fid, wt: wt, s: s}, nil
	case encoding.WireFixed64:
		u, err := dec.Uint64()
		if err != nil {
			return rawPayload{}, fmt.Errorf("%w: field %d: %s", ErrCorrupt, fid, err)
		}
		return rawPayload{id: fid, wt: wt, u: u}, nil
	case encoding.WireFixed8:
		u8, err := dec.Uint8()
		if err != nil {
			return rawPayload{}, fmt.Errorf("%w: field %d: %s", ErrCorrupt, fid, err)
		}
		return rawPayload{id: fid, wt: wt, u8: u8}, nil
	case encoding.WireList:
		// No payload uses lists in v0.10.0; skip for forward-compat.
		if err := dec.Skip(wt); err != nil {
			return rawPayload{}, fmt.Errorf("%w: field %d skip: %s", ErrCorrupt, fid, err)
		}
		return rawPayload{id: fid, wt: wt}, nil
	default:
		return rawPayload{}, fmt.Errorf("%w: field %d: unknown wire type %d", ErrCorrupt, fid, wt)
	}
}

// rawPayload holds a decoded payload field that hasn't been routed to a
// concrete event type yet. It is package-internal.
type rawPayload struct {
	id uint8
	wt encoding.WireType
	s  string
	u  uint64
	u8 uint8
}

// assemble dispatches to the per-kind builder based on the discriminator
// recovered from the record header.
func assemble(kind Kind, id string, hdr Header, fields []rawPayload) (Event, error) {
	switch kind {
	case KindEntity:
		return assembleEntity(id, hdr, fields), nil
	case KindDecision:
		return assembleDecision(id, hdr, fields)
	case KindLink:
		return assembleLink(hdr, fields), nil
	case KindObservation:
		return assembleObservation(id, hdr, fields), nil
	case KindOutcome:
		return assembleOutcome(id, hdr, fields), nil
	default:
		return nil, ErrInvalidKind
	}
}

func assembleEntity(id string, hdr Header, fields []rawPayload) *Entity {
	ent := &Entity{Hdr: hdr, EID: id}
	for _, f := range fields {
		switch f.id {
		case fieldEntityType:
			ent.Type = f.s
		case fieldEntityPath:
			ent.Path = f.s
		case fieldEntityName:
			ent.Name = f.s
			// Unknown fields skipped via captureRaw for forward-compat.
		}
	}
	return ent
}

func assembleDecision(id string, hdr Header, fields []rawPayload) (*Decision, error) {
	dec := &Decision{Hdr: hdr, DID: id}
	for _, f := range fields {
		switch f.id {
		case fieldDecisionTitle:
			dec.Title = f.s
		case fieldDecisionBody:
			dec.Body = f.s
		case fieldDecisionSeverity:
			sev := Severity(f.u8)
			if !sev.Valid() {
				return nil, fmt.Errorf("%w: %d", ErrInvalidSeverity, f.u8)
			}
			dec.Severity = sev
		case fieldDecisionScope:
			sc := Scope(f.u8)
			if !sc.Valid() {
				return nil, fmt.Errorf("%w: %d", ErrInvalidScope, f.u8)
			}
			dec.Scope = sc
		case fieldDecisionSource:
			dec.Source = f.s
		}
	}
	return dec, nil
}

func assembleLink(hdr Header, fields []rawPayload) *Link {
	l := &Link{Hdr: hdr}
	for _, f := range fields {
		switch f.id {
		case fieldLinkSrcRef:
			l.SrcRef = f.s
		case fieldLinkEdgeType:
			l.EdgeType = f.s
		case fieldLinkDstRef:
			l.DstRef = f.s
		}
	}
	return l
}

func assembleObservation(id string, hdr Header, fields []rawPayload) *Observation {
	o := &Observation{Hdr: hdr, OID: id}
	for _, f := range fields {
		switch f.id {
		case fieldObservationAction:
			o.Action = f.s
		case fieldObservationSubject:
			o.Subject = f.s
		}
	}
	return o
}

func assembleOutcome(id string, hdr Header, fields []rawPayload) *Outcome {
	x := &Outcome{Hdr: hdr, XID: id}
	for _, f := range fields {
		switch f.id {
		case fieldOutcomeType:
			x.Type = f.s
		case fieldOutcomeSubject:
			x.Subject = f.s
		}
	}
	return x
}
