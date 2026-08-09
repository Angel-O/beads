package spikeb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const (
	wireDeclarationType = "memory_beads_interchange"
	wireRecordType      = "memory_beads_record"
)

// The object-valued id is an intentional downgrade guard. The supported
// legacy Beads importers decode unknown _type records as issues whose id is a
// string, so they reject this first line during parsing and never reach a data
// record. A current decoder knows the guard's shape and validates it.
type wireGuard struct {
	RejectLegacy bool `json:"reject_legacy"`
}

type wireDeclaration struct {
	Type            string      `json:"_type"`
	ID              wireGuard   `json:"id"`
	Declaration     Declaration `json:"declaration"`
	SourceProjectID ProjectID   `json:"source_project_id"`
}

type wireRecord struct {
	Type   string `json:"_type"`
	Record Record `json:"record"`
}

// EncodeInterchange crosses the actual B1 profile boundary: one guarded
// declaration line followed by independently decoded record lines.
func EncodeInterchange(unit InterchangeUnit) ([]byte, error) {
	if err := validateUnit(unit); err != nil {
		return nil, err
	}
	lines := make([][]byte, 0, len(unit.Records)+1)
	header, err := json.Marshal(wireDeclaration{
		Type:            wireDeclarationType,
		ID:              wireGuard{RejectLegacy: true},
		Declaration:     unit.Declaration,
		SourceProjectID: unit.SourceProjectID,
	})
	if err != nil {
		return nil, err
	}
	lines = append(lines, header)
	for _, record := range unit.Records {
		line, err := json.Marshal(wireRecord{Type: wireRecordType, Record: cloneRecord(record)})
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return append(bytes.Join(lines, []byte{'\n'}), '\n'), nil
}

// DecodeInterchange rejects unknown fields and record kinds before returning
// any unit to a provider. It deliberately does not expose a streaming apply
// callback: the complete connected unit must pass preflight before mutation.
func DecodeInterchange(data []byte) (InterchangeUnit, error) {
	lines := bytes.Split(data, []byte{'\n'})
	for len(lines) > 0 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 {
		return InterchangeUnit{}, fmt.Errorf("%w: declaration and at least one record are required", ErrInvalid)
	}
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			return InterchangeUnit{}, fmt.Errorf("%w: blank lines are not canonical interchange", ErrInvalid)
		}
	}

	var header wireDeclaration
	if err := decodeStrict(lines[0], &header); err != nil {
		return InterchangeUnit{}, fmt.Errorf("%w: decode declaration: %v", ErrUnsupportedDeclaration, err)
	}
	if header.Type != wireDeclarationType || !header.ID.RejectLegacy {
		return InterchangeUnit{}, fmt.Errorf("%w: missing downgrade guard", ErrUnsupportedDeclaration)
	}
	unit := InterchangeUnit{
		Declaration:     header.Declaration,
		SourceProjectID: header.SourceProjectID,
		Records:         make([]Record, 0, len(lines)-1),
	}
	for index, line := range lines[1:] {
		var encoded wireRecord
		if err := decodeStrict(line, &encoded); err != nil {
			return InterchangeUnit{}, fmt.Errorf("%w: decode record %d: %v", ErrInvalid, index+1, err)
		}
		if encoded.Type != wireRecordType {
			return InterchangeUnit{}, fmt.Errorf("%w: record %d has type %q", ErrInvalid, index+1, encoded.Type)
		}
		unit.Records = append(unit.Records, cloneRecord(encoded.Record))
	}
	if err := validateUnit(unit); err != nil {
		return InterchangeUnit{}, err
	}
	return unit, nil
}

func validateUnit(unit InterchangeUnit) error {
	if err := validateDeclaration(unit.Declaration); err != nil {
		return err
	}
	if unit.SourceProjectID == "" {
		return fmt.Errorf("%w: source project ID is required", ErrInvalid)
	}
	records, err := recordsByID(unit.Records)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("%w: an interchange unit needs records", ErrInvalid)
	}
	for _, record := range unit.Records {
		if err := validateRecord(unit.SourceProjectID, records, record); err != nil {
			return err
		}
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
